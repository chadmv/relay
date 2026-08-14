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

// web/src uses relative imports (Windows path separators), never a POSIX-style
// alias, so this is the one place that needs normalizing for a path to read the
// same in a received-array diff on Windows and Linux.
function toPosix(path: string): string {
  return path.replace(/\\/g, '/')
}

// Strips comments before scanning, both kinds this repo writes. A previous
// version of this scan only stripped `//` line comments, and a real review lane
// broke it: inserting `/* This <Table> renders jobs. */` into a fully compliant
// consumer made the call-site guard below report it as missing minWidth, because
// the tag-matching regex stopped at the first `>` inside the comment's own prose.
// `{/* ... */}` JSX comments are `/* ... */` blocks underneath, and this repo
// writes them constantly - six landed in this diff alone - so a scan blind to
// them will eventually fire on someone documenting intent near a <Table> call or
// a grid class, and the fix people reach for under that pressure is to weaken the
// guard rather than the scan. Block comments are stripped first (they can span
// multiple lines), then `//` line comments on what remains.
function withoutComments(src: string): string {
  const noBlocks = src.replace(/\/\*[\s\S]*?\*\//g, '')
  return noBlocks
    .split('\n')
    .map((line) => (line.trim().startsWith('//') ? '' : line))
    .join('\n')
}

// Matches a bare numeric Tailwind utility suffix from 2 up to 12 - the full range
// Tailwind ships for both grid-cols-N and col-span-N. `\b` alone after a
// single-character class (the previous `[2-9]\b` shape) stops matching at the
// first digit, so it silently accepts `grid-cols-12` and `col-span-10`: exactly
// the two-digit values a wide, many-column grid is most likely to reach for.
const NUMERIC_SUFFIX = '(?:[2-9]|1[0-2])'

// THE CONVENTION, enforced rather than merely written down: a numeric Tailwind
// column count must carry a breakpoint prefix, so the layout is single-column (or
// two-column) on a narrow viewport and multi-column from `md` up. The one site that
// already did this correctly is admin/server/ServerTab.tsx; three others did not,
// and /schedules/:id overflowed a 768px viewport to 840px because of it.
//
// The lookbehind is what distinguishes `grid-cols-2` from `md:grid-cols-2`.
// `grid-cols-[...]` (arbitrary track lists) is not matched at all: a bracket
// follows the hyphen, not a digit. Those are the table templates, and the table
// call-site guard below is the rule for them.
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
    const src = withoutComments(readFileSync(file, 'utf8'))
    for (const line of src.split('\n')) {
      if (line.includes('md:grid-cols-')) continue
      for (const m of line.matchAll(new RegExp(`(?<!:)grid-cols-${NUMERIC_SUFFIX}\\b`, 'g'))) {
        offenders.push(`${toPosix(relative(SRC, file))}: ${m[0]}`)
      }
    }
  }
  expect(offenders).toEqual([])

  // Second control: the rule is satisfied by USING the prefix, not by deleting the
  // grids. Every site that was fixed must still be a multi-column grid from md up.
  const prefixed = FILES.filter((f) => readFileSync(f, 'utf8').includes('md:grid-cols-'))
    .map((f) => toPosix(relative(SRC, f)))
    .sort()
  expect(prefixed).toEqual([
    'admin/server/ServerTab.tsx',
    'admin/server/StatSection.tsx',
    'schedules/ScheduleDetailPage.tsx',
    'workers/WorkerDetailPage.tsx',
  ])
})

// THE CONVENTION FOR col-span, enforced. A bare `col-span-N` survives into a
// grid's single-column layout below `md` exactly as much as a bare `grid-cols-N`
// does, but for the opposite reason: the CONTAINER correctly went
// `grid-cols-1 ... md:grid-cols-2`, but StatSection.tsx's wide TOTAL cell modifier
// stayed the bare `col-span-2`. Below `md` the explicit grid has only ONE track,
// so `grid-column: span 2` forces an implicit second track plus a gap-3 (12px)
// gutter to satisfy a span the single-column grid was never asked for - the wide
// card rendered ~12px wider than every sibling, the exact ragged layout Task 2 of
// docs/superpowers/plans/2026-08-13-narrow-viewport-overflow.md was meant to
// remove. Neither the grid-cols guard above (which matches `grid-cols-*` only) nor
// the table call-site guard below (which matches `<Table` only) sees a
// `col-span-*` literal, so a real-browser lane had to find this one.
test('every numeric col-span carries a breakpoint prefix', () => {
  const offenders: string[] = []
  for (const file of FILES) {
    const src = withoutComments(readFileSync(file, 'utf8'))
    for (const line of src.split('\n')) {
      if (line.includes('md:col-span-')) continue
      for (const m of line.matchAll(new RegExp(`(?<!:)col-span-${NUMERIC_SUFFIX}\\b`, 'g'))) {
        offenders.push(`${toPosix(relative(SRC, file))}: ${m[0]}`)
      }
    }
  }
  expect(offenders).toEqual([])
})

// THE CONVENTION FOR TABLES is enforced by the type system now, not by a scan
// here. `TableProps.minWidth` (Table.tsx) is REQUIRED, so `tsc -b` rejects any
// call site that omits it - including an aliased import (`Table as HoloTable`)
// or a file this directory walker never reaches, which a regex-based presence
// scan could not do. The deleted version of this test (`every Table call site
// opts in to a scroll min-width`) text-matched `<Table` tags and existed only to
// enforce presence; presence is now a compile error instead of a test failure,
// and the fragility that motivated the comment-stripping and regex-widening
// fixes above (a scan is only ever as good as its pattern) no longer applies to
// this rule at all.
