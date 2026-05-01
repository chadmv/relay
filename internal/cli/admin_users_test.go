package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminUsersList_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/v1/users", r.URL.Path)
		require.Equal(t, "", r.URL.RawQuery)
		require.Equal(t, "Bearer admintoken", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":         "11111111-1111-1111-1111-111111111111",
				"email":      "admin@test.com",
				"name":       "Admin",
				"is_admin":   true,
				"created_at": "2026-04-01T12:00:00Z",
			},
			{
				"id":         "22222222-2222-2222-2222-222222222222",
				"email":      "alice@test.com",
				"name":       "Alice",
				"is_admin":   false,
				"created_at": "2026-04-02T12:00:00Z",
			},
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "list"}, &out)
	require.NoError(t, err)

	got := out.String()
	require.Contains(t, got, "admin@test.com")
	require.Contains(t, got, "alice@test.com")
	require.Contains(t, got, "Admin")
	require.Contains(t, got, "Alice")
	require.Contains(t, got, "yes") // is_admin=true
	require.Contains(t, got, "no")  // is_admin=false
}

func TestAdminUsersList_NotLoggedIn(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "list"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not logged in")
}

func TestAdminUsersGet_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/v1/users", r.URL.Path)
		require.Equal(t, "email=alice%40test.com", r.URL.RawQuery)
		require.Equal(t, "Bearer admintoken", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":         "22222222-2222-2222-2222-222222222222",
				"email":      "alice@test.com",
				"name":       "Alice",
				"is_admin":   false,
				"created_at": "2026-04-02T12:00:00Z",
			},
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "get", "alice@test.com"}, &out)
	require.NoError(t, err)

	got := out.String()
	require.Contains(t, got, "ID:")
	require.Contains(t, got, "22222222-2222-2222-2222-222222222222")
	require.Contains(t, got, "Email:")
	require.Contains(t, got, "alice@test.com")
	require.Contains(t, got, "Name:")
	require.Contains(t, got, "Alice")
	require.Contains(t, got, "Admin:")
	require.Contains(t, got, "no")
	require.Contains(t, got, "Created:")
}

func TestAdminUsersGet_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "get", "nobody@test.com"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "user not found: nobody@test.com")
}

func TestAdminUsersGet_MissingEmail(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "get"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage:")
}

func TestAdminUsersGet_NotLoggedIn(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "get", "alice@test.com"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not logged in")
}

func TestAdminUsersUpdate_ByUUID(t *testing.T) {
	const targetID = "22222222-2222-2222-2222-222222222222"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "PATCH", r.Method)
		require.Equal(t, "/v1/users/"+targetID, r.URL.Path)
		require.Equal(t, "Bearer admintoken", r.Header.Get("Authorization"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "Renamed", body["name"])

		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         targetID,
			"email":      "alice@test.com",
			"name":       "Renamed",
			"is_admin":   false,
			"created_at": "2026-04-02T12:00:00Z",
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "update", targetID, "--name", "Renamed"}, &out)
	require.NoError(t, err)
	require.Contains(t, out.String(), "Renamed")
}

func TestAdminUsersUpdate_ByEmail(t *testing.T) {
	const targetID = "22222222-2222-2222-2222-222222222222"
	var calls []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/users":
			require.Equal(t, "email=alice%40test.com", r.URL.RawQuery)
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id":         targetID,
				"email":      "alice@test.com",
				"name":       "Alice",
				"is_admin":   false,
				"created_at": "2026-04-02T12:00:00Z",
			}})
		case r.Method == "PATCH" && r.URL.Path == "/v1/users/"+targetID:
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, "Renamed", body["name"])
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         targetID,
				"email":      "alice@test.com",
				"name":       "Renamed",
				"is_admin":   false,
				"created_at": "2026-04-02T12:00:00Z",
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "update", "alice@test.com", "--name", "Renamed"}, &out)
	require.NoError(t, err)
	require.Len(t, calls, 2)
	require.Contains(t, calls[0], "GET /v1/users")
	require.Contains(t, calls[1], "PATCH /v1/users/"+targetID)
}

func TestAdminUsersUpdate_EmailNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method, "should not call PATCH when email lookup misses")
		require.Equal(t, "/v1/users", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "update", "nobody@test.com", "--name", "x"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "user not found: nobody@test.com")
}

func TestAdminUsersUpdate_EmptyName(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "update", "alice@test.com", "--name", ""}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--name is required")
}

func TestAdminUsersUpdate_MissingPositional(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "update"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage: relay admin users update")
}

func TestAdminUsersUpdate_NotLoggedIn(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "update", "alice@test.com", "--name", "x"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not logged in")
}

func TestAdminUsersUpdate_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const targetID = "22222222-2222-2222-2222-222222222222"
		require.Equal(t, "PATCH", r.Method)
		require.Equal(t, "/v1/users/"+targetID, r.URL.Path)
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "admin only"})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "usertoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{
		"users", "update",
		"22222222-2222-2222-2222-222222222222",
		"--name", "x",
	}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "admin only")
}

func TestAdminUsersCreate_HappyPath(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/users", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedBody))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":         "00000000-0000-0000-0000-000000000001",
			"email":      capturedBody["email"],
			"name":       capturedBody["name"],
			"is_admin":   capturedBody["is_admin"],
			"created_at": time.Now(),
		})
	}))
	defer srv.Close()

	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		return "newpassword1", nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: srv.URL, Token: "admin-token"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{
		"users", "create",
		"--email", "ops@test.com",
		"--name", "Ops Bot",
	}, &out)
	require.NoError(t, err)

	assert.Equal(t, "ops@test.com", capturedBody["email"])
	assert.Equal(t, "Ops Bot", capturedBody["name"])
	assert.Equal(t, "newpassword1", capturedBody["password"])
	assert.Equal(t, false, capturedBody["is_admin"])
	assert.Contains(t, out.String(), "ops@test.com")
}

func TestAdminUsersCreate_AdminFlag(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedBody))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":         "00000000-0000-0000-0000-000000000002",
			"email":      capturedBody["email"],
			"name":       capturedBody["email"],
			"is_admin":   true,
			"created_at": time.Now(),
		})
	}))
	defer srv.Close()

	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		return "adminpass1", nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: srv.URL, Token: "admin-token"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{
		"users", "create",
		"--email", "newadmin@test.com",
		"--admin",
	}, &out)
	require.NoError(t, err)
	assert.Equal(t, true, capturedBody["is_admin"])
}

func TestAdminUsersCreate_MissingEmail(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "admin-token"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "create"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--email")
}

func TestAdminUsersCreate_PasswordMismatch(t *testing.T) {
	callCount := 0
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		callCount++
		if callCount == 1 {
			return "first1234", nil
		}
		return "second1234", nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: "http://localhost", Token: "admin-token"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{
		"users", "create",
		"--email", "x@test.com",
	}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "passwords do not match")
}

func TestAdminUsersCreate_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "email already registered"})
	}))
	defer srv.Close()

	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		return "anypassword1", nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: srv.URL, Token: "admin-token"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{
		"users", "create",
		"--email", "dup@test.com",
	}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "email already registered")
}

func TestAdminUsersCreate_NotLoggedIn(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost"} // no Token
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{
		"users", "create",
		"--email", "x@test.com",
	}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not logged in")
}
