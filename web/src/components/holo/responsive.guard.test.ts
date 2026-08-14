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

// Strips lines whose trimmed content starts with `//`, so a prose comment naming a
// JSX tag (e.g. Table.tsx's own header comment saying "<Table> call site") is not
// counted as that tag's usage. Line comments only, matching every comment this repo
// actually writes in these files; a `/* */` block would need a different pass, and
// none of the files this scan targets uses one for prose that names a component tag.
function withoutLineComments(src: string): string {
  return src
    .split('\n')
    .map((line) => (line.trim().startsWith('//') ? '' : line))
    .join('\n')
}

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

// THE CONVENTION FOR TABLES, enforced. Every <Table> call site passes minWidth, so
// the eleventh table cannot silently reintroduce
// docs/backlog/bug-2026-08-12-web-narrow-viewport-horizontal-overflow.md. A comment
// in the primitive would not have caught the tenth.
test('every Table call site opts in to a scroll min-width', () => {
  const opens: string[] = []
  const missing: string[] = []
  let bareCount = 0
  for (const file of FILES) {
    // Drop `//` comment lines before scanning: Table.tsx's own header comment
    // (Task 3) mentions "<Table>" in prose twice, and a naive text scan of the
    // whole file counts those as call sites, which is not what "every call site"
    // means. Every real call site in this codebase is JSX, never inside a line
    // comment.
    const src = withoutLineComments(readFileSync(file, 'utf8'))
    bareCount += [...src.matchAll(/<Table\b/g)].length
    for (const m of src.matchAll(/<Table\b[\s\S]*?>/g)) {
      const where = relative(SRC, file).replace(/\\/g, '/')
      opens.push(where)
      if (!m[0].includes('minWidth')) missing.push(where)
    }
  }
  // Control 1: the tag regex matched every occurrence. It stops at the first '>',
  // so an inline arrow function in the props (`onSort={(f) => ...}`) would truncate
  // a tag - no consumer does that today, and if one starts, the truncated tag will
  // almost certainly lack minWidth and fail loudly below rather than silently pass.
  expect(opens).toHaveLength(bareCount)
  // Control 2: the scan found the consumers rather than nothing at all.
  expect(opens.length).toBeGreaterThanOrEqual(10)
  expect(missing).toEqual([])
})
