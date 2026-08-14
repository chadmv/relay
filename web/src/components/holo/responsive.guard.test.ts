import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
// Explicit node:url URL, not the global: the test environment is jsdom, which
// shadows the global URL constructor with its own (whatwg-url) implementation.
// That implementation cannot resolve a relative path against a file:// base that
// carries a Windows drive letter - it silently falls back to jsdom's default
// document location (http://localhost:3000/) instead of throwing, so the bug
// surfaces one line later as "The URL must be of scheme file" out of
// fileURLToPath. Node's own URL has no such bug.
import { fileURLToPath, URL as NodeURL } from 'node:url'
import { expect, test } from 'vitest'

// web/src/ - this file lives at web/src/components/holo/.
const SRC = fileURLToPath(new NodeURL('../../', import.meta.url))

function sourceFiles(dir: string): string[] {
  const out: string[] = []
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      out.push(...sourceFiles(full))
      continue
    }
    // Shipped JSX only. Test files render deliberately unrealistic markup.
    if (!entry.endsWith('.tsx') || entry.endsWith('.test.tsx')) continue
    out.push(full)
  }
  return out
}

const FILES = sourceFiles(SRC)

// THE CONVENTION, enforced rather than merely written down: a numeric Tailwind
// column count must carry a breakpoint prefix, so the layout is single-column (or
// two-column) on a narrow viewport and multi-column from `md` up. The one site that
// already did this correctly is admin/server/ServerTab.tsx; three others did not,
// and /schedules/:id overflowed a 768px viewport to 840px because of it.
//
// The lookbehind is what distinguishes `grid-cols-2` from `md:grid-cols-2`.
// `grid-cols-[...]` (arbitrary track lists) is not matched at all: a bracket
// follows the hyphen, not a digit. Those are the table templates, and Task 4's
// guard below is the rule for them.
//
// The check is PER LINE, not per file: WorkerDetailPage's KPI row is deliberately
// two-up at the narrow default and four-up from `md` (grid-cols-2 ... md:grid-cols-4)
// rather than one-up, because stacking four short stat cards singly pushes the page
// body a screen down for no gain (Decision 3 of
// docs/superpowers/plans/2026-08-13-narrow-viewport-overflow.md). That pattern's
// base track count is itself a bare, unprefixed grid-cols-N, so a whole-file "no bare
// occurrence anywhere" rule would flag a line that already carries a working
// breakpoint override. A bare count is only an offender when ITS OWN line has no
// md:grid-cols- anywhere on it - which still catches a fifth site added with no
// breakpoint at all, the case this test exists to prevent.
test('every numeric grid column count carries a breakpoint prefix', () => {
  // Control: the walker found the tree, not an empty directory. A silent zero here
  // would make the assertion below pass vacuously.
  expect(FILES.length).toBeGreaterThan(50)

  const offenders: string[] = []
  for (const file of FILES) {
    const src = readFileSync(file, 'utf8')
    for (const line of src.split('\n')) {
      if (line.includes('md:grid-cols-')) continue
      for (const m of line.matchAll(/(?<!:)grid-cols-[2-9]\b/g)) {
        offenders.push(`${relative(SRC, file)}: ${m[0]}`)
      }
    }
  }
  expect(offenders).toEqual([])

  // Second control: the rule is satisfied by USING the prefix, not by deleting the
  // grids. Every site that was fixed must still be a multi-column grid from md up.
  const prefixed = FILES.filter((f) => readFileSync(f, 'utf8').includes('md:grid-cols-'))
    .map((f) => relative(SRC, f).replace(/\\/g, '/'))
    .sort()
  expect(prefixed).toEqual([
    'admin/server/ServerTab.tsx',
    'admin/server/StatSection.tsx',
    'schedules/ScheduleDetailPage.tsx',
    'workers/WorkerDetailPage.tsx',
  ])
})
