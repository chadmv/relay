import { readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'
import ts from 'typescript'
// Explicit node:url URL, not the global: the test environment is jsdom, which
// shadows the global URL constructor with its own (whatwg-url) implementation.
// That implementation cannot resolve a relative path against a file:// base that
// carries a Windows drive letter - it silently falls back to jsdom's default
// document location (http://localhost:3000/) instead of throwing, so the bug
// surfaces one line later as "The URL must be of scheme file" out of
// fileURLToPath. Node's own URL has no such bug.
import { fileURLToPath, URL as NodeURL } from 'node:url'

// web/src/ - this file lives at web/src/test/.
export const SRC_ROOT = fileURLToPath(new NodeURL('../', import.meta.url))

// Excluded from every walk. These are test harness modules, not shipped UI:
// renderWithQuery renders deliberately unrealistic markup for other tests to
// mount, so a rule about what the app ships must not be evaluated against it.
const TEST_DIR = join(SRC_ROOT, 'test')

// Shipped sources with the given extensions, e.g. ['.tsx'] or ['.ts', '.tsx'].
// Extensions are a parameter because one guard rules on JSX only while another
// must also reach hooks and plain modules. Declaration files are excluded: they
// carry no implementation for any rule to be about.
export function shippedSources(extensions: string[]): string[] {
  return walk(SRC_ROOT, extensions)
}

function walk(dir: string, extensions: string[]): string[] {
  const out: string[] = []
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      if (full === TEST_DIR) continue
      out.push(...walk(full, extensions))
      continue
    }
    if (entry.endsWith('.d.ts')) continue
    if (!extensions.some((ext) => entry.endsWith(ext))) continue
    if (extensions.some((ext) => entry.endsWith(`.test${ext}`))) continue
    out.push(full)
  }
  return out
}

// web/src uses relative imports (Windows path separators), never a POSIX-style
// alias, so this is the one place that needs normalizing for a path to read the
// same in a received-array diff on Windows and Linux.
export function toPosix(path: string): string {
  return path.replace(/\\/g, '/')
}

// Strips comments before scanning, both from .ts files and from JSX
// `{/* ... */}` comments in .tsx files, by parsing the source with
// TypeScript's own parser rather than re-deriving comment and string
// boundaries by hand: a regex literal, JSX text as its own lexical run, and
// a `/` after a closing brace are each a real lexical state a hand-rolled
// scanner has to model correctly, and the parser already does. Every
// comment range TypeScript itself reports (leading or trailing, on every
// node including tokens, so a dangling comment inside an empty JSX
// expression container like `{/* ... */}` is still reached - pinned by a
// dedicated test) is blanked in place, non-newline characters replaced with
// spaces, keeping newlines so line numbers in the returned text still match
// the source.
//
// KNOWN LIMIT: a file that fails to parse throws, naming the file, rather
// than silently stripping nothing - a broken producer should fail this
// guard loud, not pass it by accident.
function collectCommentRanges(sourceFile: ts.SourceFile, fullText: string): Array<{ pos: number; end: number }> {
  const seen = new Set<number>()
  const ranges: Array<{ pos: number; end: number }> = []
  function addRanges(rs: ts.CommentRange[] | undefined) {
    if (!rs) return
    for (const r of rs) {
      if (seen.has(r.pos)) continue
      seen.add(r.pos)
      ranges.push({ pos: r.pos, end: r.end })
    }
  }
  function visit(node: ts.Node) {
    addRanges(ts.getLeadingCommentRanges(fullText, node.getFullStart()))
    addRanges(ts.getTrailingCommentRanges(fullText, node.getEnd()))
    node.getChildren(sourceFile).forEach(visit)
  }
  visit(sourceFile)
  return ranges
}

export function withoutComments(src: string, fileName = '<anonymous>.tsx'): string {
  const sourceFile = ts.createSourceFile(fileName, src, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  // parseDiagnostics is not part of the public API surface in the .d.ts, but
  // is present at runtime; it is what distinguishes a genuine syntax error
  // from a clean parse that simply has no comments.
  const diagnostics = (sourceFile as unknown as { parseDiagnostics?: readonly ts.Diagnostic[] }).parseDiagnostics
  if (diagnostics && diagnostics.length > 0) {
    const message = ts.flattenDiagnosticMessageText(diagnostics[0].messageText, ' ')
    throw new Error(`withoutComments: ${fileName} failed to parse: ${message}`)
  }
  const ranges = collectCommentRanges(sourceFile, src)
  const chars = src.split('')
  for (const { pos, end } of ranges) {
    for (let i = pos; i < end; i++) {
      if (chars[i] !== '\n') chars[i] = ' '
    }
  }
  return chars.join('')
}
