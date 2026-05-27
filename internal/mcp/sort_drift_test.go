package mcp

import (
	"testing"

	"relay/internal/api"

	"github.com/stretchr/testify/assert"
)

// mcpSortKeys enumerates the sort values each MCP list tool advertises in
// its jsonschema description. Keep these in sync with the description
// strings -- the test asserts every value here is in the server's
// allowlist for the matching endpoint.
var (
	jobsMCPSortKeys = []string{
		"created_at", "-created_at",
		"name", "-name",
		"priority", "-priority",
		"status", "-status",
		"updated_at", "-updated_at",
	}
	workersMCPSortKeys = []string{
		"created_at", "-created_at",
		"name", "-name",
		"status", "-status",
		"last_seen_at", "-last_seen_at",
	}
	schedulesMCPSortKeys = []string{
		"created_at", "-created_at",
		"name", "-name",
		"next_run_at", "-next_run_at",
		"updated_at", "-updated_at",
	}
	reservationsMCPSortKeys = []string{
		"created_at", "-created_at",
		"name", "-name",
		"starts_at", "-starts_at",
		"ends_at", "-ends_at",
	}
)

// TestMCPSortDescriptionsMatchServerAllowlist asserts every (key, direction)
// value advertised in an MCP tool's sort description is actually accepted
// by the corresponding server-side SortSpec. Drift (a key removed from the
// server but still listed in the MCP doc, or a new server key not surfaced
// to the LLM) fails CI.
func TestMCPSortDescriptionsMatchServerAllowlist(t *testing.T) {
	cases := []struct {
		name       string
		mcpKeys    []string
		serverSpec api.SortSpec
	}{
		{"jobs", jobsMCPSortKeys, api.JobsSortSpec},
		{"workers", workersMCPSortKeys, api.WorkersSortSpec},
		{"schedules", schedulesMCPSortKeys, api.ScheduledJobsSortSpec},
		{"reservations", reservationsMCPSortKeys, api.ReservationsSortSpec},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range tc.mcpKeys {
				base := k
				if len(k) > 0 && k[0] == '-' {
					base = k[1:]
				}
				_, ok := tc.serverSpec.Keys[base]
				assert.True(t, ok,
					"MCP advertises sort key %q for %s but server allowlist omits %q",
					k, tc.name, base)
			}

			// Also verify every server key is surfaced to MCP (catches the
			// other direction of drift).
			for base := range tc.serverSpec.Keys {
				foundAsc := false
				foundDesc := false
				for _, k := range tc.mcpKeys {
					if k == base {
						foundAsc = true
					}
					if k == "-"+base {
						foundDesc = true
					}
				}
				assert.True(t, foundAsc,
					"server allowlist has %q for %s but MCP doesn't advertise %q",
					base, tc.name, base)
				assert.True(t, foundDesc,
					"server allowlist has %q for %s but MCP doesn't advertise %q",
					base, tc.name, "-"+base)
			}
		})
	}
}
