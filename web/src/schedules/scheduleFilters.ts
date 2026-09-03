// The Schedule status filter's three chips, and the single place their labels
// and their wire values are decided.
//
// `enabled` on GET /v1/scheduled-jobs is a genuine tri-state, not a boolean flag:
// the server's parseScheduleFilters reads an empty value as absent and otherwise
// produces a pointer to the parsed bool, so `enabled=false` is the real request
// "only paused schedules" and is NOT the same as sending nothing. A mapping that
// collapses 'disabled' to undefined turns the Disabled chip into a second All
// chip, with no error and no visible difference in the list envelope.
export const ENABLED_FILTERS = [
  { key: 'all', label: 'All' },
  { key: 'enabled', label: 'Enabled' },
  { key: 'disabled', label: 'Disabled' },
] as const

export type EnabledFilterKey = (typeof ENABLED_FILTERS)[number]['key']

// undefined means "omit the parameter entirely", which the caller must honour by
// not calling URLSearchParams.set at all - `enabled=` (an empty value) is read as
// absent server-side, so it would work, but an omitted key is what the query key
// and the request agree on.
export function enabledParam(key: EnabledFilterKey): 'true' | 'false' | undefined {
  switch (key) {
    case 'enabled':
      return 'true'
    case 'disabled':
      return 'false'
    case 'all':
      return undefined
  }
}
