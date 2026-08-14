package main

import (
	"testing"
	"time"

	"relay/internal/worker"

	"github.com/stretchr/testify/require"
)

// No build tag: this is a pure function and it runs under `make test`, unlike
// every other test file in this package.
func TestParseTrailingLogWindow(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		want   time.Duration
		wantOK bool
	}{
		{"unset keeps the default and does NOT warn", "", worker.DefaultTrailingLogWindow, true},
		{"a valid duration is used", "45m", 45 * time.Minute, true},
		{"the documented escape hatch is honoured", "8760h", 8760 * time.Hour, true},
		{"unparseable keeps the default and warns", "fifteen minutes", worker.DefaultTrailingLogWindow, false},
		{"zero keeps the default and warns", "0s", worker.DefaultTrailingLogWindow, false},
		{"negative keeps the default and warns", "-5m", worker.DefaultTrailingLogWindow, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseTrailingLogWindow(tc.raw)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.wantOK, ok,
				"ok drives whether main logs a startup warning; warning on the ordinary unset case is as wrong as staying silent on a typo")
		})
	}
}
