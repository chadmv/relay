import { readFileSync } from 'node:fs'
import { relative } from 'node:path'
import { expect, test } from 'vitest'
import { SRC_ROOT, shippedSources, toPosix, withoutComments } from '../../test/sourceTree'

// The walk, the comment stripper and the path normalizer are shared with the
// dialog module's guard; sourceTree.ts carries the reason each one has the shape
// it has. The set this file rules over is JSX only.
const SRC = SRC_ROOT
const FILES = shippedSources(['.tsx'])

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
