package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
)

// listQueryError writes a list endpoint's existing 500 body and logs one line
// naming the underlying failure.
//
// IT EXISTS BECAUSE OF THE STATEMENT TIMEOUT. A statement cancelled by
// RELAY_DB_STATEMENT_TIMEOUT reaches here as SQLSTATE 57014, and this slice
// deliberately does NOT give it a distinguishable response - a timed-out search
// answers 500 like any other database failure. Without a log line, a control
// that fires is a control nobody can observe firing.
//
// A PgError renders its Code and Message and NOTHING ELSE. Detail, Hint and
// Where can quote a parameter value, and on these two routes the parameter is a
// caller-supplied needle: rendering it would put caller input into an operator's
// log pipeline. r.URL.Path, never r.URL.String(), for the same reason.
func listQueryError(w http.ResponseWriter, r *http.Request, err error, msg string) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		log.Printf("%s %s: %s (SQLSTATE %s)", r.Method, r.URL.Path, pgErr.Message, pgErr.Code)
	} else {
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	}
	writeError(w, http.StatusInternalServerError, msg)
}
