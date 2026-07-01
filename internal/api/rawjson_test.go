package api

import "testing"

// rawObject normalizes JSON *object* fields (env, requires) so a client never
// receives a bare `null` where an object is expected. json.Marshal of a nil
// map is `null`, which is what an omitted env/requires ends up stored as; the
// old passthrough returned that null and crashed the web job-detail page.
func TestRawObject(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"nil", nil, "{}"},
		{"empty", []byte{}, "{}"},
		{"json null", []byte("null"), "{}"},
		{"object passthrough", []byte(`{"A":"B"}`), `{"A":"B"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(rawObject(c.in)); got != c.want {
				t.Fatalf("rawObject(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
