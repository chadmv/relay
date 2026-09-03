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
// The absolute path derived from SHELL, so every reader of the shell's own
// file shares one source of truth instead of repeating the join.
const SHELL_PATH = join(SRC_ROOT, SHELL)

function rel(file: string): string {
  return toPosix(relative(SRC_ROOT, file))
}

// Memoized per file: every assertion below calls stripped() once per file it
// scans, so without this the whole tree would be parsed once per assertion
// rather than once per run.
const strippedCache = new Map<string, string>()
function stripped(file: string): string {
  const cached = strippedCache.get(file)
  if (cached !== undefined) return cached
  const result = withoutComments(readFileSync(file, 'utf8'), file)
  strippedCache.set(file, result)
  return result
}

// C0. Shared by every assertion below, and the reason they are not vacuous. A
// silent zero, or a walk that quietly started including test files, would make
// every absence assertion in this file pass forever while proving nothing.
// One representative path per top-level directory under web/src that ships
// sources, plus App.tsx for the loose top-level files, anchored the same way
// shell/HoloShell.tsx already was. A floor on the total count alone would
// tolerate an entire directory silently dropping out of the walk as long as
// the survivors still cleared it.
const TOP_LEVEL_ANCHORS = [
  'App.tsx',
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

// PERMANENT KILLS for withoutComments itself, not for any assertion above it:
// the defect lives in sourceTree.ts, so each is pinned directly against the
// shared helper rather than through a consumer.
test('a slash-star inside a string literal does not swallow the producer after it', () => {
  const src = "const ACCEPT = 'image/*'\nconst x = <div role=\"dialog\" />\n/* trailing note */\n"
  expect(withoutComments(src)).toContain('role="dialog"')
})

test('an apostrophe inside a comment does not pair with an unrelated string', () => {
  const src =
    "function Comp() {\n  return (\n    <>\n      {/* the header's own stacking context */}\n      " +
    "<b title='x' />\n      <div role=\"dialog\" aria-modal=\"true\" />\n    </>\n  )\n}\n" +
    '/* trailing note */\n'
  const result = withoutComments(src)
  expect(result).toContain('role="dialog"')
  expect(result).toContain('aria-modal="true"')
})

test('a backtick inside a comment does not pair with an unrelated template literal', () => {
  const src =
    '/* see the ` SCRIM identifier */\n' +
    'function Comp() {\n  const tpl = `y`;\n  return <div role="dialog" aria-modal="true" />\n}\n' +
    '/* trailing note */\n'
  expect(withoutComments(src)).toContain('role="dialog"')
})

test('a regex literal holding a quote inside a character class is not mistaken for a string', () => {
  const src = "const P = /role=\\{?['\"`]dialog['\"`]/; const x = <div role=\"dialog\" />\n"
  expect(withoutComments(src)).toContain('role="dialog"')
})

test('a regex literal spelling an escaped slash-star is not mistaken for a comment', () => {
  const src = "const P = /\\/\\*[\\s\\S]*?\\*\\//g; const x = <div role=\"dialog\" />\n"
  expect(withoutComments(src)).toContain('role="dialog"')
})

// A regex literal following an arrow function's body, or the return
// keyword, is a real position a text-based heuristic can misread as
// division - and the regex's own content can then open a fake line comment
// that eats the rest of the line.
test('a regex literal after a filter callback does not hide the producer after it', () => {
  const src =
    "function Comp() {\n  const xs = ['a'].filter((p) => /[/*]/.test(p));\n  " +
    'return <div role="dialog" aria-modal="true" />\n}\n/* trailing note */\n'
  expect(withoutComments(src)).toContain('role="dialog"')
})

test('a regex literal after return does not hide a producer on the same line', () => {
  const src = 'function f() { return /[//]/ } const x = <div role="dialog" />\n'
  expect(withoutComments(src)).toContain('role="dialog"')
})

// JSX text is not JavaScript, so a character that would open a string or a
// comment in real code is just literal text there. A scanner with no lexical
// state for JSX text reads it the same way it would read code, which is
// wrong for exactly this reason.
test('an unbalanced backtick in JSX text does not swallow a later comment', () => {
  const src =
    'function Comp() {\n  return (\n    <>\n      <p>the `SCRIM value</p>\n      ' +
    '<div role="dialog" />\n    </>\n  )\n}\n' +
    '// comment mentioning aria-modal must not leak the attribute name as code\n'
  const result = withoutComments(src)
  expect(result).toContain('role="dialog"')
  expect(result).not.toContain('aria-modal')
})

test('a URL in JSX text does not hide a producer on the same line', () => {
  const src = 'function Comp() { return (<><p>see http://x.example</p><div role="dialog" /></>) }\n'
  expect(withoutComments(src)).toContain('role="dialog"')
})

test('an apostrophe in JSX text does not retain a trailing comment', () => {
  const src = "<p>don't</p> // comment naming the role\n"
  expect(withoutComments(src)).not.toContain('role')
})

// A `/` immediately after a closing brace (a JSX expression container's own
// `}`) is a real position a text-based heuristic can misread as a regex
// start, which then eats the real block comment that follows.
test('a slash after a closing brace does not eat a real block comment', () => {
  const src = '<span>{1} / {2} {/* comment naming aria-modal */}</span>\n'
  expect(withoutComments(src)).not.toContain('aria-modal')
})

// Verifies the parser-based walk reaches a comment with no code on either
// side of it inside a JSX expression container - the shape DialogShell.tsx's
// own header and every {/* ... */} comment in this codebase use.
test('a comment inside an empty JSX expression container is reached', () => {
  const src =
    'function Comp() {\n  return (\n    <>\n      <div>{/* aria-modal should not leak */}</div>\n      ' +
    '<div role="dialog" />\n    </>\n  )\n}\n'
  const result = withoutComments(src)
  expect(result).toContain('role="dialog"')
  expect(result).not.toContain('aria-modal')
})

test('a file that fails to parse throws naming the file, rather than stripping nothing', () => {
  expect(() => withoutComments('const x = (\n', 'broken-producer.tsx')).toThrow('broken-producer.tsx')
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
const SHELL_SRC = readFileSync(SHELL_PATH, 'utf8')
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
// caught. KNOWN GAP: only a trailing plus is joined - a leading plus on the
// following line instead is not.
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
  const shellStripped = stripped(SHELL_PATH)
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
  'a scoped document Escape gated on being topmost. For a non-modal disclosure, follow the existing ' +
  'entries and add one stating why this surface is not a modal.'

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
  // Pins that the probe matches EITHER receiver: a document.-only pattern
  // would miss a hook that registers on window instead.
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
  'insertAdjacentText',
  'replaceChildren',
  'innerHTML',
  'outerHTML',
]
const PORTAL_PROBE = new RegExp(
  `createPortal|document\\s*\\.\\s*body\\s*\\.\\s*(?:${BODY_INSERTIONS.join('|')})\\b`,
)

// Files outside DIALOG_DIR that are nonetheless permitted to portal or
// insert onto the body, each with the reason its layer needs that route
// rather than moving under components/dialog/. Any entry that the probe
// stops matching goes red below, naming the entry to delete.
const PORTAL_ALLOWED: string[] = []

const BODY_PORTAL =
  'a portal or a document.body insertion outside the dialog module. If it lands on the body while a ' +
  'dialog is open, the dialog stack marks background children on register and unregister only, so ' +
  'the new node is never marked inert or aria-hidden and paints above the scrim as a later sibling. ' +
  'Move it under components/dialog/, or add an entry to PORTAL_ALLOWED stating why this layer needs ' +
  'to portal from outside the module, and decide at the same time whether it sits above or below a ' +
  'modal.'

test('the enumerated body-insertion methods are all in the probe', () => {
  // Each of these is a distinct route onto document.body from
  // appendChild/append/prepend/insertBefore, so each needs its own entry to
  // be matched. innerHTML and outerHTML are property assignments, not calls
  // - the probe only requires a word boundary after the name, so it does not
  // care which shape follows.
  const snippets: Record<string, string> = {
    appendChild: 'document.body.appendChild(x)',
    append: 'document.body.append(x)',
    prepend: 'document.body.prepend(x)',
    insertBefore: 'document.body.insertBefore(x, y)',
    insertAdjacentElement: 'document.body.insertAdjacentElement(x)',
    insertAdjacentHTML: 'document.body.insertAdjacentHTML(x)',
    insertAdjacentText: 'document.body.insertAdjacentText(x)',
    replaceChildren: 'document.body.replaceChildren(x)',
    innerHTML: 'document.body.innerHTML = x',
    outerHTML: 'document.body.outerHTML = x',
  }
  for (const [method, snippet] of Object.entries(snippets)) {
    expect(BODY_INSERTIONS, `${method} is missing from BODY_INSERTIONS`).toContain(method)
    expect(PORTAL_PROBE.test(snippet), `probe does not match ${method}`).toBe(true)
  }
})

test('a portal outside the dialog module is under it or allowlisted with a reason', () => {
  const found = SOURCES.filter((f) => PORTAL_PROBE.test(stripped(f))).map(rel)
  // C5.
  expect(found, 'the portal probe no longer matches the shell').toContain(SHELL)
  expect(found, 'the portal probe no longer matches the stack').toContain(STACK)

  // Same staleness control as DOC_KEYDOWN_ALLOWED: every allowlisted file
  // must still be matched by the probe, so a drifted entry goes red instead
  // of silently exempting a file that no longer needs the exception.
  expect(
    PORTAL_ALLOWED.filter((p) => !found.includes(p)),
    'the portal probe no longer matches a file PORTAL_ALLOWED says needs the exception - delete the entry',
  ).toEqual([])

  const outside = found.filter((p) => !p.startsWith(DIALOG_DIR))
  expect(outside.filter((p) => !PORTAL_ALLOWED.includes(p)), BODY_PORTAL).toEqual([])
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
  // A bare negative value's own leading hyphen is not a compound-identifier
  // hyphen, so it must still match. Built from fragments, never spelled as
  // one contiguous class-shaped literal.
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
