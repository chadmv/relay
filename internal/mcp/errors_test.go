package mcp

import (
	"errors"
	"testing"

	"relay/internal/relayclient"

	"github.com/stretchr/testify/require"
)

func TestMapError_HTTPCodes(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{401, "auth_expired"},
		{403, "forbidden"},
		{404, "not_found"},
		{400, "validation"},
		{409, "conflict"},
		{429, "rate_limited"},
		{500, "server_error"},
		{502, "server_error"},
	}
	for _, tc := range cases {
		err := &relayclient.ResponseError{StatusCode: tc.status, Message: "boom"}
		got := MapError(err)
		require.Equal(t, tc.want, got.Code, "status=%d", tc.status)
		require.Equal(t, "boom", got.Message)
		require.NotEmpty(t, got.Hint, "hint should be set for status=%d", tc.status)
	}
}

func TestMapError_NetworkError(t *testing.T) {
	got := MapError(errors.New("dial tcp: connection refused"))
	require.Equal(t, "network", got.Code)
	require.Contains(t, got.Hint, "could not reach server")
}

func TestMapError_Nil(t *testing.T) {
	require.Nil(t, MapError(nil))
}
