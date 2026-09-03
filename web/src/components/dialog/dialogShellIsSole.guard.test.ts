import { readFileSync } from 'node:fs'
import { join, relative } from 'node:path'
import { expect, test } from 'vitest'
import { SRC_ROOT, shippedSources, toPosix, withoutComments } from '../../test/sourceTree'

// THE MODAL SHELL IS THE ONLY MODAL. Every rule here says the same thing from a
// different angle: modal semantics, the scrim, a document-level Escape, and a
// body-level portal all belong to this module, because each of them is a global
// side effect that two independent owners cannot both get right. A hand-rolled
// dialog happens because its author had no reason to open another author's
// files - so the rule has to arrive on the commit that breaks it, not in a
// header note.
//
// Every assertion strips comments first and rules over shipped source only. Both
// exclusions are load-bearing and for different reasons: role and aria-modal are
// spelled in test files that assert them, and both are spelled a second time in
// DialogShell.tsx's own header comment.
//
// KNOWN GAPS, stated rather than implied: these are text scans and are only as
// good as their patterns. A role assembled from a variable, a body reference
// obtained by some other route, and a stacking order set through an inline style
// are all invisible here. The scans raise the cost of a silent violation; they do
// not make one impossible.
//
// NOTHING IN THIS FILE SPELLS A UTILITY CLASS. Tailwind v4 scans prose under
// web/, so a class-shaped literal in a test file emits CSS and keeps a rule alive
// independently of the component that owns it. Class-shaped probes are either
// built from fragments or extracted from the source at run time.

const TSX = shippedSources(['.tsx'])
const SOURCES = shippedSources(['.ts', '.tsx'])

const SHELL = 'components/dialog/DialogShell.tsx'
const STACK = 'components/dialog/dialogStack.ts'
const DIALOG_DIR = 'components/dialog/'

function rel(file: string): string {
  return toPosix(relative(SRC_ROOT, file))
}

function stripped(file: string): string {
  return withoutComments(readFileSync(file, 'utf8'))
}

// C0. Shared by every assertion below, and the reason they are not vacuous. A
// silent zero, or a walk that quietly started including test files, would make
// every absence assertion in this file pass forever while proving nothing.
// One representative path per top-level directory under web/src that ships
// sources, anchored the same way shell/HoloShell.tsx already was. A floor on
// the total count alone would tolerate an entire directory silently dropping
// out of the walk as long as the survivors still cleared it.
const TOP_LEVEL_ANCHORS = [
  SHELL,
  'admin/invites/inviteStatus.ts',
  'app/router.tsx',
  'auth/LoginScreen.tsx',
  'jobs/JobsPage.tsx',
  'lib/api.ts',
  'profile/SessionsTab.tsx',
  'schedules/ScheduleDetailPage.tsx',
  'shell/HoloShell.tsx',
  'workers/WorkerDetailPage.tsx',
]

test('the source walk reaches shipped sources and nothing else', () => {
  expect(TSX.length).toBeGreaterThan(50)
  expect(SOURCES.length).toBeGreaterThan(TSX.length)

  const paths = SOURCES.map(rel)
  expect(paths).toContain(SHELL)
  expect(paths).toContain(STACK)
  expect(
    TOP_LEVEL_ANCHORS.filter((p) => !paths.includes(p)),
    'the walk no longer reaches one of these top-level directories',
  ).toEqual([])
  expect(paths.filter((p) => p.startsWith('test/')), 'the walk reached a test harness module').toEqual([])
  expect(paths.filter((p) => /\.test\.tsx?$/.test(p)), 'the walk reached a test file').toEqual([])
})

// PERMANENT KILL for withoutComments itself, not for any assertion above it.
// The block-comment stripper is not string-aware, so a slash-star run inside a
// string literal (an accept-attribute value like 'image/*' is the shape that
// surfaced this) reads as an unterminated block comment and swallows every
// producer between it and the next real */, or to EOF if there is none -
// silently hiding a hand-rolled violation from every assertion in this file.
// Reproduced directly against the shared helper, because the defect is in
// sourceTree.ts, not in any one consumer.
test('a slash-star inside a string literal does not swallow the producer after it', () => {
  const src = "const ACCEPT = 'image/*'\nconst x = <div role=\"dialog\" />\n/* trailing note */\n"
  expect(withoutComments(src)).toContain('role="dialog"')
})

// A1. The probe accepts the string form and the braced form, so a role written
// as an expression is caught too. A role assembled from a variable is not - see
// the header's known gaps.
const ROLE_PROBE = /role=\{?['"`]dialog['"`]/

const HAND_ROLLED_MODAL =
  'compose DialogShell instead of declaring modal semantics on a div. The shell owns the portal, ' +
  'the focus trap, the scoped Escape and registration in the dialog stack, and a hand-rolled modal ' +
  'gets none of them.'

test('the modal role is declared in the dialog shell only', () => {
  const found = TSX.filter((f) => ROLE_PROBE.test(stripped(f))).map(rel)
  // C1: the probe matches the one file that is supposed to carry it. Without
  // this, a probe that matches nothing satisfies the rule below vacuously.
  expect(found, 'the role probe no longer matches DialogShell').toContain(SHELL)
  expect(found.filter((p) => p !== SHELL), HAND_ROLLED_MODAL).toEqual([])
})

// A2. The test-file exclusion, not any argument about query helpers, is what
// makes this work: test files spell this attribute literally in
// toHaveAttribute assertions. It is also spelled a second time inside
// DialogShell.tsx's own header comment, which is why comments are stripped even
// for the file that legitimately carries it.
const MODAL_ATTR_PROBE = /aria-modal\s*=/

test('the modal attribute is declared in the dialog shell only', () => {
  const found = TSX.filter((f) => MODAL_ATTR_PROBE.test(stripped(f))).map(rel)
  // C2.
  expect(found, 'the aria-modal probe no longer matches DialogShell').toContain(SHELL)
  expect(found.filter((p) => p !== SHELL), HAND_ROLLED_MODAL).toEqual([])
})

// A3. The scrim string is never spelled here. It is extracted from the shell's
// own source at run time, anchored on the SCRIM identifier - an identifier is not
// class-shaped, so nothing in this file emits CSS, and the probe follows the
// value if it changes.
const SHELL_SRC = readFileSync(join(SRC_ROOT, 'components', 'dialog', 'DialogShell.tsx'), 'utf8')
const SCRIM_MATCH = SHELL_SRC.match(/\bSCRIM\s*=\s*'([^']*)'/)
const SCRIM_VALUE = SCRIM_MATCH ? SCRIM_MATCH[1] : ''
const SCRIM_TOKENS = SCRIM_VALUE.split(/\s+/).filter(Boolean)
// The three tokens a near miss has to reproduce: the two the value leads with,
// plus the stacking-order token wherever it sits. Requiring the stacking token
// specifically is what keeps a line carrying two common layout tokens from
// registering.
const STACK_PREFIX = 'z' + '-'
const SCRIM_DISTINCTIVE = [
  SCRIM_TOKENS[0],
  SCRIM_TOKENS[1],
  SCRIM_TOKENS.find((t) => t.startsWith(STACK_PREFIX)) ?? '',
]

const HAND_ROLLED_SCRIM =
  'this is the modal scrim. Compose DialogShell rather than painting one: the scrim is half of a ' +
  'modal and the stack that owns the scroll lock, the inert background and the Escape scoping is ' +
  'the other half.'

// Multi-line string concatenations (this repo's own convention for a long
// class string - see GlassPanel.tsx's BASE) split a class across physical
// lines, so a per-line-only pass cannot see all three distinctive tokens on
// any ONE line. This second pass joins a trailing plus-then-newline run back
// into the line before it and re-checks, so a scrim rebuilt that way is still
// caught.
function joinConcatenatedLines(text: string): string {
  return text.replace(/\s*\+\s*\n\s*/g, ' ')
}

function hasDistinctiveLine(text: string): boolean {
  const perLine = (t: string) =>
    t.split('\n').some((line) => SCRIM_DISTINCTIVE.every((tok) => line.includes(tok)))
  return perLine(text) || perLine(joinConcatenatedLines(text))
}

test('the modal scrim is painted by the dialog shell only', () => {
  // C3: the extraction actually produced a value with enough tokens to be
  // discriminating, and both sub-probes match the file that owns it.
  expect(SCRIM_TOKENS.length, 'the SCRIM extraction found nothing').toBeGreaterThanOrEqual(5)
  expect(SCRIM_DISTINCTIVE.every(Boolean), 'no stacking-order token in the scrim value').toBe(true)
  const shellStripped = stripped(join(SRC_ROOT, 'components', 'dialog', 'DialogShell.tsx'))
  expect(shellStripped).toContain(SCRIM_VALUE)
  expect(hasDistinctiveLine(shellStripped)).toBe(true)

  const others = SOURCES.filter((f) => rel(f) !== SHELL)

  // Exact: nobody else holds the whole string.
  const exact = others.filter((f) => stripped(f).includes(SCRIM_VALUE)).map(rel)
  expect(exact, HAND_ROLLED_SCRIM).toEqual([])

  // Near miss: nobody else has a line (or a concatenation-joined line)
  // carrying all three distinctive tokens. This is what catches a scrim
  // rebuilt with the tokens reordered, with one added, or split across a
  // multi-line concatenation, none of which the exact probe can see.
  const near = others.filter((f) => hasDistinctiveLine(stripped(f))).map(rel)
  expect(near, HAND_ROLLED_SCRIM).toEqual([])
})

test('the near-miss probe also catches a scrim split across a concatenation', () => {
  // Permanent kill: a scrim rebuilt as a multi-line string concatenation put
  // the distinctive tokens on separate physical lines, which the per-line-only
  // pass could not see. Tokens come from the real extraction above, never
  // spelled.
  const [first, second, third] = SCRIM_DISTINCTIVE
  const split = `const x = '${first} filler ' +\n  'more ${second} filler ${third} end'`
  expect(hasDistinctiveLine(split)).toBe(true)
})

// A4. Anchored on document. or window. so a listener registered on some other
// receiver (an AbortSignal, for instance) is not a false positive. Widened to
// plain modules as well as JSX, and to window as well as document: a hook
// registering on either would otherwise be invisible to a narrower probe.
const DOC_KEYDOWN_PROBE = /(?:document|window)\s*\.\s*addEventListener\s*\(\s*['"`]keydown['"`]/

// THE ALLOWLIST is per FILE, not per listener: an allowlisted file may
// register more than one document- or window-level keydown listener and this
// passes by design, because the reason column is about the SURFACE, not a
// per-listener count. An entry may only be added with the reason that surface
// is not a modal, because the reason is the actual control - a bare count
// would grow silently.
//
//  components/dialog/DialogShell.tsx - the modal shell itself. Escape must fire
//    when focus has left the panel, which a panel-scoped handler cannot see.
//  shell/UserMenu.tsx - a disclosure, not a modal: no scrim, no scroll lock, no
//    inert background, no focus trap, and Tab out is a dismiss route.
//  shell/HoloShell.tsx - the collapsed navigation panel, the sibling disclosure
//    of the above, sharing its handler set.
const DOC_KEYDOWN_ALLOWED = [SHELL, 'shell/UserMenu.tsx', 'shell/HoloShell.tsx']

const UNLISTED_KEYDOWN =
  'a document- or window-level Escape handler. For a modal, compose DialogShell, which already owns ' +
  'a scoped document Escape gated on being topmost. For a non-modal disclosure, follow the two ' +
  'existing ones and add an allowlist entry stating why this surface is not a modal.'

test('every file with a document or window keydown listener is allowlisted with a reason', () => {
  const found = SOURCES.filter((f) => DOC_KEYDOWN_PROBE.test(stripped(f))).map(rel)

  // C4, and the strongest control in this file: every allowlisted file is
  // matched by the probe. If the probe drifts, an entry stops matching and this
  // goes red - which a count-shaped control could never detect.
  expect(
    DOC_KEYDOWN_ALLOWED.filter((p) => !found.includes(p)),
    'the keydown probe no longer matches a file the allowlist says registers one',
  ).toEqual([])

  expect(found.filter((p) => !DOC_KEYDOWN_ALLOWED.includes(p)), UNLISTED_KEYDOWN).toEqual([])
})

test('the keydown probe also catches a window-level listener', () => {
  // Permanent kill: window.addEventListener('keydown', ...) was invisible to
  // the document.-only probe before this fix round.
  expect(DOC_KEYDOWN_PROBE.test("window.addEventListener('keydown', onKey)")).toBe(true)
})

// CONSIDERED AND DELIBERATELY NOT ASSERTED: both disclosures also register a
// document mousedown listener. A fourth of those is not the hazard this file is
// about, and asserting it would make the allowlist grow for a reason unrelated
// to modal semantics.

// A5. Route 1 of the body-level portal question is a constraint written in
// dialogStack.ts's header; this is what delivers it to the person who needs it,
// on the commit that needs it. The probe stays broad on purpose: createPortal
// matches regardless of target, because DialogShell.tsx's own call passes the
// layer element, not the literal document.body expression - narrowing the
// probe to a body-targeted createPortal call would stop matching the shell
// itself and silently break C5 below. KNOWN GAP: a body reference obtained
// some other way is not caught.
const BODY_INSERTIONS = [
  'appendChild',
  'append',
  'prepend',
  'insertBefore',
  'insertAdjacentElement',
  'insertAdjacentHTML',
  'replaceChildren',
]
const PORTAL_PROBE = new RegExp(
  `createPortal|document\\s*\\.\\s*body\\s*\\.\\s*(?:${BODY_INSERTIONS.join('|')})\\b`,
)

const BODY_PORTAL =
  'a portal or a document.body insertion outside the dialog module. If it lands on the body while a ' +
  'dialog is open, the dialog stack marks background children on register and unregister only, so ' +
  'the new node is never marked inert or aria-hidden and paints above the scrim as a later sibling. ' +
  'Portal into a container this component owns, or add the entry here and decide at the same time ' +
  'whether the new layer sits above or below a modal.'

test('the portal probe catches every enumerated body-insertion method', () => {
  // Permanent kill for the three methods missing before this fix round:
  // insertAdjacentElement, insertAdjacentHTML and replaceChildren are all
  // routes onto document.body that the earlier enumeration did not cover.
  for (const method of ['insertAdjacentElement', 'insertAdjacentHTML', 'replaceChildren']) {
    expect(BODY_INSERTIONS, `${method} is missing from BODY_INSERTIONS`).toContain(method)
    expect(PORTAL_PROBE.test(`document.body.${method}(x)`), `probe does not match ${method}`).toBe(true)
  }
})

test('any portal outside the dialog module needs an entry', () => {
  const found = SOURCES.filter((f) => PORTAL_PROBE.test(stripped(f))).map(rel)
  // C5.
  expect(found, 'the portal probe no longer matches the shell').toContain(SHELL)
  expect(found, 'the portal probe no longer matches the stack').toContain(STACK)
  expect(found.filter((p) => !p.startsWith(DIALOG_DIR)), BODY_PORTAL).toEqual([])
})

// A6. A document with nothing keeping it honest is the same defect as an
// invariant with nothing enforcing it, one level up - which is the defect this
// file exists to fix. Both sides are derived: the tree side builds its pattern
// from fragments, and the document side parses the ENTRY lines out of the block,
// so neither spells a utility class. KNOWN GAP: a stacking order set through an
// inline style rather than a utility is invisible to this.
const Z = 'z' + '-'
const Z_INDEX = Z + 'index'
// The leading minus is captured, not excluded by the lookbehind: the
// lookbehind still checks the character BEFORE the optional sign, so a
// variant-prefixed positive value keeps matching (preceded by the variant's
// own colon) while a bare negative value also matches (preceded by nothing,
// whitespace or a quote), and a value glued onto a preceding word still does
// not (preceded by a word character). The sign is folded into the recorded
// value so it lines up with ENTRY_LINE's own signed capture.
const Z_NUMERIC = new RegExp(`(?<![\\w-])(-?)${Z}(\\d+)(?![\\w-])`, 'g')
const Z_ARBITRARY = new RegExp(`(?<![\\w-])-?${Z}\\[`)
const TOKENS_CSS = join(SRC_ROOT, 'theme', 'tokens.css')
const ENTRY_LINE = new RegExp(`^\\s*ENTRY\\s+${Z_INDEX}\\s+(-?\\d+)\\s+(\\S+\\.tsx?)\\b`)

function scannedPairs(): string[] {
  const out = new Set<string>()
  for (const file of SOURCES) {
    for (const m of stripped(file).matchAll(Z_NUMERIC)) out.add(`${rel(file)} ${m[1]}${m[2]}`)
  }
  return [...out].sort()
}

test('the stacking-order scan reads a negative value with its sign', () => {
  // Permanent kill: a bare negative numeric value and a bare negative
  // arbitrary value both survived before this fix round, because the
  // lookbehind treated the sign's own hyphen as disqualifying. Built from
  // fragments, never spelled as one contiguous class-shaped literal.
  const negativeNumeric = '-' + Z + '10'
  const negativeArbitrary = '-' + Z + '[100]'
  const numeric = [...negativeNumeric.matchAll(Z_NUMERIC)]
  expect(numeric.map((m) => m[1] + m[2])).toEqual(['-10'])
  expect(Z_ARBITRARY.test(negativeArbitrary)).toBe(true)
})

function documentedPairs(): string[] {
  const css = readFileSync(TOKENS_CSS, 'utf8')
  const start = css.indexOf('LAYERING SCALE - begin')
  const end = css.indexOf('LAYERING SCALE - end')
  if (start < 0 || end < 0) return []
  const out = new Set<string>()
  for (const line of css.slice(start, end).split('\n')) {
    const m = line.match(ENTRY_LINE)
    if (m) out.add(`${m[2]} ${m[1]}`)
  }
  return [...out].sort()
}

const DOCUMENT_IT =
  'a stacking order that the LAYERING SCALE block in theme/tokens.css does not name. Add an ENTRY ' +
  'line for it - the surface, the value, the owning file, the symbol carrying it, and which ' +
  'stacking context it lives in - and read rule 3 while you are there: a value is comparable only ' +
  'to another value in the same context.'

test('the layering block names every stacking order in shipped source', () => {
  const scanned = scannedPairs()
  const documented = documentedPairs()

  // C6.
  expect(scanned.length, 'the stacking-order scan found nothing').toBeGreaterThan(0)
  expect(documented.length, 'no ENTRY line parsed out of the layering block').toBeGreaterThan(0)
  expect(documented.some((p) => p.startsWith(SHELL))).toBe(true)
  expect(documented.some((p) => p.startsWith('shell/HoloShell.tsx'))).toBe(true)

  expect(scanned.filter((p) => !documented.includes(p)), DOCUMENT_IT).toEqual([])
  expect(
    documented.filter((p) => !scanned.includes(p)),
    'the layering block names a stacking order the tree no longer has. Delete the ENTRY line.',
  ).toEqual([])

  // An arbitrary bracketed value cannot be expressed as an ENTRY number, so it
  // is refused outright rather than slipping past the pair comparison.
  expect(
    SOURCES.filter((f) => Z_ARBITRARY.test(stripped(f))).map(rel),
    'an arbitrary stacking-order value. Give it a plain number, add its ENTRY line, and widen this ' +
      'guard deliberately if the arbitrary form is really needed.',
  ).toEqual([])
})
