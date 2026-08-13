package api

import (
	"reflect"
	"strings"
	"testing"

	"relay/internal/store"

	"github.com/stretchr/testify/assert"
)

// The two list endpoints must never be able to return a stored token hash. The
// control is column enumeration in the .sql files: with token_hash absent from
// every SELECT, the generated row types have no field for it, so returning it
// is a compile error rather than a review miss.
//
// This test turns that structural property into an assertion. Adding
// `i.token_hash` to any of these queries changes the generated struct and turns
// this red at the next `go test ./...`, with no Docker required.
func TestListEndpointRowTypesHaveExactProjections(t *testing.T) {
	cases := []struct {
		name   string
		typ    reflect.Type
		fields []string
	}{
		{"ListInvitesPageRow", reflect.TypeOf(store.ListInvitesPageRow{}),
			[]string{"ID", "Email", "CreatedBy", "CreatedAt", "ExpiresAt", "UsedAt", "CreatedByEmail"}},
		{"ListInvitesPageByCreatedAscRow", reflect.TypeOf(store.ListInvitesPageByCreatedAscRow{}),
			[]string{"ID", "Email", "CreatedBy", "CreatedAt", "ExpiresAt", "UsedAt", "CreatedByEmail"}},
		{"ListInvitesPageByExpiresDescRow", reflect.TypeOf(store.ListInvitesPageByExpiresDescRow{}),
			[]string{"ID", "Email", "CreatedBy", "CreatedAt", "ExpiresAt", "UsedAt", "CreatedByEmail"}},
		{"ListInvitesPageByExpiresAscRow", reflect.TypeOf(store.ListInvitesPageByExpiresAscRow{}),
			[]string{"ID", "Email", "CreatedBy", "CreatedAt", "ExpiresAt", "UsedAt", "CreatedByEmail"}},
		{"ListActiveTokensForUserPageRow", reflect.TypeOf(store.ListActiveTokensForUserPageRow{}),
			[]string{"ID", "CreatedAt", "ExpiresAt"}},
		{"ListActiveTokensForUserPageByCreatedAscRow", reflect.TypeOf(store.ListActiveTokensForUserPageByCreatedAscRow{}),
			[]string{"ID", "CreatedAt", "ExpiresAt"}},
	}

	for _, tc := range cases {
		got := make([]string, 0, tc.typ.NumField())
		for i := 0; i < tc.typ.NumField(); i++ {
			name := tc.typ.Field(i).Name
			got = append(got, name)
			assert.NotContains(t, strings.ToLower(name), "token",
				"%s must not project any token-bearing column", tc.name)
		}
		assert.ElementsMatch(t, tc.fields, got,
			"%s projection changed; if this is intentional, update the response mapper and the item key-set tests in the same change", tc.name)
	}
}
