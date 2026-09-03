package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

type reservationResponse struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Selector  json.RawMessage `json:"selector"`
	WorkerIDs []string        `json:"worker_ids"`
	UserID    string          `json:"user_id"`
	Project   *string         `json:"project,omitempty"`
	StartsAt  *time.Time      `json:"starts_at,omitempty"`
	EndsAt    *time.Time      `json:"ends_at,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

func toReservationResponse(res store.Reservation) reservationResponse {
	workerIDs := make([]string, len(res.WorkerIds))
	for i, wid := range res.WorkerIds {
		workerIDs[i] = uuidStr(wid)
	}

	var startsAt *time.Time
	if res.StartsAt.Valid {
		t := res.StartsAt.Time
		startsAt = &t
	}
	var endsAt *time.Time
	if res.EndsAt.Valid {
		t := res.EndsAt.Time
		endsAt = &t
	}

	return reservationResponse{
		ID:        uuidStr(res.ID),
		Name:      res.Name,
		Selector:  rawJSON(res.Selector),
		WorkerIDs: workerIDs,
		UserID:    uuidStr(res.UserID),
		Project:   res.Project,
		StartsAt:  startsAt,
		EndsAt:    endsAt,
		CreatedAt: res.CreatedAt.Time,
	}
}

var ReservationsSortSpec = SortSpec{
	Default: "-created_at",
	Keys: map[string]SortKeyKind{
		"created_at": SortKeyTimestamp,
		"name":       SortKeyText,
		"starts_at":  SortKeyTimestamp,
		"ends_at":    SortKeyTimestamp,
	},
}

func reservationsRowKey(res store.Reservation) (anySortVal, pgtype.UUID) {
	return res.CreatedAt.Time, res.ID
}

func reservationsRowKeyByName(res store.Reservation) (anySortVal, pgtype.UUID) {
	return res.Name, res.ID
}

func reservationsRowKeyByStarts(res store.Reservation) (anySortVal, pgtype.UUID) {
	if !res.StartsAt.Valid {
		return (*time.Time)(nil), res.ID
	}
	t := res.StartsAt.Time
	return &t, res.ID
}

func reservationsRowKeyByEnds(res store.Reservation) (anySortVal, pgtype.UUID) {
	if !res.EndsAt.Valid {
		return (*time.Time)(nil), res.ID
	}
	t := res.EndsAt.Time
	return &t, res.ID
}

// reservationFilters carries the one optional GET /v1/reservations predicate in
// the type the generated sqlc Params field uses. The zero value means "no filter
// active": an invalid pgtype.UUID sends SQL NULL, which the predicate reads as
// "match everything".
type reservationFilters struct {
	WorkerID pgtype.UUID
}

// reservationFilterParams are the query parameters parseReservationFilters
// reads. handleListReservations passes them to rejectRepeatedParams before
// calling in.
var reservationFilterParams = []string{"worker_id"}

// parseReservationFilters produces the optional GET /v1/reservations predicate.
// On invalid input it writes the response itself and returns ok=false.
//
// An id that names no worker is NOT an error: reservations.worker_ids is a bare
// UUID[] with no foreign key, so a worker id can outlive its row and this
// endpoint cannot distinguish "never existed" from "deleted". It answers an
// empty page.
//
// qs is the query string parsePage already parsed and arity-checked; see
// parseFilterQ for why it is passed rather than re-read.
func parseReservationFilters(w http.ResponseWriter, qs url.Values) (reservationFilters, bool) {
	var f reservationFilters

	if raw := qs.Get("worker_id"); raw != "" {
		id, err := parseUUID(raw)
		if err != nil {
			// Renders nothing input-derived, unlike handleCreateReservation's
			// echo of the supplied value. Pinned by
			// TestParseReservationFilters_ErrorDoesNotEchoTheInput.
			writeError(w, http.StatusBadRequest, "invalid worker_id; expected a UUID")
			return reservationFilters{}, false
		}
		f.WorkerID = id
	}

	return f, true
}

func (s *Server) handleListReservations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r, ReservationsSortSpec)
	if !ok {
		return
	}

	if !rejectRepeatedParams(w, pp.Query, reservationFilterParams...) {
		return
	}
	filters, ok := parseReservationFilters(w, pp.Query)
	if !ok {
		return
	}

	total, err := s.q.CountReservations(ctx, filters.WorkerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count reservations failed")
		return
	}

	var items []reservationResponse
	var next string

	switch pp.Sort {
	case "-created_at":
		rows, err := s.q.ListReservationsPage(ctx, store.ListReservationsPageParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			WorkerID:  filters.WorkerID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list reservations failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toReservationResponse, reservationsRowKey)

	case "created_at":
		rows, err := s.q.ListReservationsPageByCreatedAsc(ctx, store.ListReservationsPageByCreatedAscParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			WorkerID:  filters.WorkerID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list reservations failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toReservationResponse, reservationsRowKey)

	case "-name":
		rows, err := s.q.ListReservationsPageByNameDesc(ctx, store.ListReservationsPageByNameDescParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			WorkerID:  filters.WorkerID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list reservations failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toReservationResponse, reservationsRowKeyByName)

	case "name":
		rows, err := s.q.ListReservationsPageByNameAsc(ctx, store.ListReservationsPageByNameAscParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			WorkerID:  filters.WorkerID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list reservations failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toReservationResponse, reservationsRowKeyByName)

	case "-starts_at":
		rows, err := s.q.ListReservationsPageByStartsDesc(ctx, store.ListReservationsPageByStartsDescParams{
			CursorSet:    pp.Cursor.Set,
			CursorIsNull: pp.Cursor.IsNull,
			CursorTs:     pp.CursorTs(),
			CursorID:     pp.Cursor.ID,
			WorkerID:     filters.WorkerID,
			PageLimit:    pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list reservations failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toReservationResponse, reservationsRowKeyByStarts)

	case "starts_at":
		rows, err := s.q.ListReservationsPageByStartsAsc(ctx, store.ListReservationsPageByStartsAscParams{
			CursorSet:    pp.Cursor.Set,
			CursorIsNull: pp.Cursor.IsNull,
			CursorTs:     pp.CursorTs(),
			CursorID:     pp.Cursor.ID,
			WorkerID:     filters.WorkerID,
			PageLimit:    pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list reservations failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toReservationResponse, reservationsRowKeyByStarts)

	case "-ends_at":
		rows, err := s.q.ListReservationsPageByEndsDesc(ctx, store.ListReservationsPageByEndsDescParams{
			CursorSet:    pp.Cursor.Set,
			CursorIsNull: pp.Cursor.IsNull,
			CursorTs:     pp.CursorTs(),
			CursorID:     pp.Cursor.ID,
			WorkerID:     filters.WorkerID,
			PageLimit:    pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list reservations failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toReservationResponse, reservationsRowKeyByEnds)

	case "ends_at":
		rows, err := s.q.ListReservationsPageByEndsAsc(ctx, store.ListReservationsPageByEndsAscParams{
			CursorSet:    pp.Cursor.Set,
			CursorIsNull: pp.Cursor.IsNull,
			CursorTs:     pp.CursorTs(),
			CursorID:     pp.Cursor.ID,
			WorkerID:     filters.WorkerID,
			PageLimit:    pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list reservations failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toReservationResponse, reservationsRowKeyByEnds)

	default:
		panic("handleListReservations: missing dispatch arm for sort key " + pp.Sort)
	}

	writeJSON(w, http.StatusOK, page[reservationResponse]{Items: items, NextCursor: next, Total: total})
}

func (s *Server) handleCreateReservation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, ok := UserFromCtx(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var body struct {
		Name      string            `json:"name"`
		Selector  map[string]string `json:"selector"`
		UserID    string            `json:"user_id"`
		Project   *string           `json:"project"`
		StartsAt  *time.Time        `json:"starts_at"`
		EndsAt    *time.Time        `json:"ends_at"`
		WorkerIDs []string          `json:"worker_ids"`
	}
	if !readJSON(w, r, &body) {
		return
	}

	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	selectorJSON, err := json.Marshal(body.Selector)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal selector failed")
		return
	}

	// Determine user ID — use provided or fall back to authenticated user
	userID := u.ID
	if body.UserID != "" {
		parsed, err := parseUUID(body.UserID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid user_id")
			return
		}
		userID = parsed
	}

	// Parse worker IDs
	workerIDs := make([]pgtype.UUID, len(body.WorkerIDs))
	for i, wid := range body.WorkerIDs {
		parsed, err := parseUUID(wid)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid worker_id: "+wid)
			return
		}
		workerIDs[i] = parsed
	}

	var startsAt pgtype.Timestamptz
	if body.StartsAt != nil {
		startsAt = pgtype.Timestamptz{Valid: true, Time: *body.StartsAt}
	}
	var endsAt pgtype.Timestamptz
	if body.EndsAt != nil {
		endsAt = pgtype.Timestamptz{Valid: true, Time: *body.EndsAt}
	}

	res, err := s.q.CreateReservation(ctx, store.CreateReservationParams{
		Name:      body.Name,
		Selector:  selectorJSON,
		WorkerIds: workerIDs,
		UserID:    userID,
		Project:   body.Project,
		StartsAt:  startsAt,
		EndsAt:    endsAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create reservation failed")
		return
	}

	writeJSON(w, http.StatusCreated, toReservationResponse(res))
}

func (s *Server) handleDeleteReservation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid reservation id")
		return
	}

	// Check existence first
	_, err = s.q.GetReservation(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "reservation not found")
		return
	}

	if err := s.q.DeleteReservation(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete reservation failed")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
