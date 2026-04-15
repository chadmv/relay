package api

import (
	"encoding/json"
	"net/http"
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

func (s *Server) handleListReservations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reservations, err := s.q.ListReservations(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list reservations failed")
		return
	}

	resp := make([]reservationResponse, len(reservations))
	for i, res := range reservations {
		resp[i] = toReservationResponse(res)
	}
	writeJSON(w, http.StatusOK, resp)
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
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
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
