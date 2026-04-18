package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

var bcryptCost = 12

var (
	dummyHashOnce   sync.Once
	dummyBcryptHash []byte
)

func getDummyHash() []byte {
	dummyHashOnce.Do(func() {
		dummyBcryptHash, _ = bcrypt.GenerateFromPassword([]byte("relay-auth-sentinel"), bcryptCost)
	})
	return dummyBcryptHash
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) issueToken(userID pgtype.UUID) (string, time.Time, error) {
	return "", time.Time{}, nil
}
