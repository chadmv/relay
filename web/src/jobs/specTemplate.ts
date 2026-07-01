// Prefilled starter spec: a minimal, valid, single-task job the user edits. An
// unedited submit succeeds and demonstrates the POST /v1/jobs shape. Uses the
// single `command` form and omits optional fields on purpose.
export const STARTER_TEMPLATE = `{
  "name": "my-job",
  "priority": "normal",
  "tasks": [
    {
      "name": "hello",
      "command": ["echo", "hello world"]
    }
  ]
}
`

export type SpecCheck =
  | { ok: true; value: unknown }
  | { ok: false; error: string }

// Minimal client-side pre-check. Deliberately shallow: valid JSON, a non-empty
// string `name`, and a non-empty `tasks` array. Deeper rules (unique task names,
// command xor commands, dependency cycles, priority enum, source) are left to
// the server (jobspec.Validate) so the two paths cannot drift.
export function validateSpecText(text: string): SpecCheck {
  let value: unknown
  try {
    value = JSON.parse(text)
  } catch (e) {
    return { ok: false, error: `Invalid JSON: ${(e as Error).message}` }
  }

  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return { ok: false, error: 'Spec must be a JSON object.' }
  }
  const obj = value as Record<string, unknown>

  if (typeof obj.name !== 'string' || obj.name.trim() === '') {
    return { ok: false, error: 'Spec is missing a non-empty "name".' }
  }
  if (!Array.isArray(obj.tasks) || obj.tasks.length === 0) {
    return { ok: false, error: 'Spec must have a non-empty "tasks" array.' }
  }

  return { ok: true, value }
}
