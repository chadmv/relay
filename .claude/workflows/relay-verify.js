export const meta = {
  name: 'relay-verify',
  description: 'Phase 4 verification: fan out the relay code reviewer across dimensions plus the integration tester, in parallel, and return consolidated findings.',
  whenToUse: 'After an implementation phase, to verify a diff against the relay Invariants, security, and integration behavior before merge.',
  phases: [
    { title: 'Verify' },
  ],
}

// args (optional): { base?: string, scope?: string }
//   base  - git ref to diff against (default: 'main')
//   scope - free-text description of what changed, passed to each agent
const base = (args && args.base) || 'main'
const scope = (args && args.scope) || 'the current working-tree diff'

const FINDINGS_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['lens', 'findings'],
  properties: {
    lens: { type: 'string' },
    findings: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['severity', 'file', 'summary', 'suggestion'],
        properties: {
          severity: { type: 'string', enum: ['high', 'medium', 'low'] },
          file: { type: 'string' },
          summary: { type: 'string' },
          suggestion: { type: 'string' },
        },
      },
    },
  },
}

phase('Verify')

const REVIEW_LENSES = [
  {
    key: 'invariants',
    prompt: `Review the diff (git diff ${base}...HEAD) covering ${scope}. Focus ONLY on the six relay Invariants: epoch fence, single job-spec pipeline, one bounded sender per gRPC stream, identity-checked teardown, no interior pointers across locks, single JSON entry point. Report any path that sidesteps an invariant. Return findings.`,
  },
  {
    key: 'correctness',
    prompt: `Review the diff (git diff ${base}...HEAD) covering ${scope} for correctness bugs and needless complexity. Run the /code-review skill if helpful. Return findings.`,
  },
  {
    key: 'security',
    prompt: `Security-review the diff (git diff ${base}...HEAD) covering ${scope}. Run the /security-review skill. Check token hashing goes through internal/tokenhash.Hash, auth/authorization paths, and input validation. Return findings.`,
  },
]

const tasks = REVIEW_LENSES.map((lens) => () =>
  agent(lens.prompt, {
    label: `review:${lens.key}`,
    phase: 'Verify',
    agentType: 'relay-code-reviewer',
    schema: FINDINGS_SCHEMA,
  })
)

tasks.push(() =>
  agent(
    `Run the relay integration tests relevant to ${scope}. Use go test -tags integration -p 1 with a 120s timeout. Report any failures or flakes as findings (severity high for failures, medium for flakes).`,
    {
      label: 'integration-tests',
      phase: 'Verify',
      agentType: 'relay-integration-tester',
      schema: FINDINGS_SCHEMA,
    }
  )
)

const results = (await parallel(tasks)).filter(Boolean)

const allFindings = results.flatMap((r) =>
  (r.findings || []).map((f) => ({ ...f, lens: r.lens }))
)
const high = allFindings.filter((f) => f.severity === 'high')

log(`relay-verify complete: ${allFindings.length} findings (${high.length} high) across ${results.length} lenses`)

return { clean: allFindings.length === 0, findings: allFindings, high }
