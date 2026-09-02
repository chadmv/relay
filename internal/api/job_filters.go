package api

import (
	"github.com/jackc/pgx/v5/pgtype"
)

// jobFilters carries the four optional GET /v1/jobs predicates in the exact
// types the generated sqlc Params fields use, so a call site spreads them
// without conversion. The zero value means "no filter active": a nil Q and an
// invalid OwnerID/Since/Until each send SQL NULL, which the predicates read as
// "match everything".
type jobFilters struct {
	Q       *string
	OwnerID pgtype.UUID
	Since   pgtype.Timestamptz
	Until   pgtype.Timestamptz
}
