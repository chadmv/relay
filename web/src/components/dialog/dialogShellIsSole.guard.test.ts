import { readFileSync } from 'node:fs'
import { relative } from 'node:path'
import { expect, test } from 'vitest'
import { SRC_ROOT, shippedSources, toPosix, withoutComments } from '../../test/sourceTree'

// THE MODAL SHELL IS THE ONLY MODAL. Every rule here says the same thing from a
// different angle: modal semantics, the scrim, a document-level Escape, and a
// body-level portal all belong to this module, because each of them is a global
// side effect that two independent owners cannot both get right. The app reached
// three hand-rolled dialogs before DialogShell existed, and it reached them
// because each author had no reason to open the others' files - so the rule has
// to arrive on the commit that breaks it, not in a header note.
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
test('the source walk reaches shipped sources and nothing else', () => {
  expect(TSX.length).toBeGreaterThan(50)
  expect(SOURCES.length).toBeGreaterThan(TSX.length)

  const paths = SOURCES.map(rel)
  expect(paths).toContain(SHELL)
  expect(paths).toContain(STACK)
  expect(paths).toContain('shell/HoloShell.tsx')
  expect(paths.filter((p) => p.startsWith('test/')), 'the walk reached a test harness module').toEqual([])
  expect(paths.filter((p) => /\.test\.tsx?$/.test(p)), 'the walk reached a test file').toEqual([])
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
// makes this work: four test files spell this attribute literally in
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
