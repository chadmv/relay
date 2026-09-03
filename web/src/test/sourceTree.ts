import { readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'
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

// Strips comments before scanning, both kinds this repo writes: `{/* ... */}`
// JSX comments are `/* ... */` blocks underneath. A single-pass scanner over
// explicit lexical states - CODE, a line comment, a block comment, a
// single-quoted string, a double-quoted string, and a template literal (with
// its own nested CODE state for each `${...}` interpolation) - rather than
// two passes over the whole file, because a two-pass blank-then-strip design
// cannot tell a quote INSIDE a comment from a real string boundary: an
// apostrophe in a comment can pair with an unrelated real string later in the
// file, or a comment's own closing `*/` gets blanked away as if it were
// inside that fabricated string, and either way the block-comment scan then
// runs past the comment's real end looking for the next `*/`, silently
// deleting real code up to it. A regex literal is recognised heuristically
// (a `/` immediately after certain operator-shaped characters, or at the
// start of a line) and its content is scanned to an unescaped closing `/`
// outside a character class, so a pattern that itself contains a quote or a
// slash-star is not mistaken for a string or a comment.
//
// Comment bodies are blanked (their content removed) but their newlines are
// kept, so line numbers in the returned text match the source - a per-line
// scan downstream sees the same line count whether or not a match happens to
// sit next to a multi-line comment.
//
// KNOWN LIMITS. The regex heuristic is conservative, not a parser: it bails
// to treating `/` as division the moment it would otherwise run past a
// newline, so a genuine division immediately followed by a quote on the same
// line could still mis-tokenise the rest of that line - pinned by the regex
// kills below, which cover the two forms this repo actually writes.
type Mode = 'code' | 'line-comment' | 'block-comment' | 'single' | 'double' | 'template'

interface Frame {
  mode: Mode
  // Set only on the CODE frame pushed for a template literal's `${...}`
  // interpolation, so the matching `}` that closes it (as opposed to any
  // `{`/`}` pair nested inside the expression) can be told apart.
  braceDepth?: number
}

// A `/` immediately after one of these is treated as a possible regex start
// rather than division; `''` covers the very start of the scan.
const REGEX_PRECEDING = new Set(['(', ',', '=', ':', '[', '!', '&', '|', '?', '{', '}', ';', '\n', ''])

// `start` points at the opening `/`. Returns the index just past the closing
// `/` (outside a character class, unescaped) plus any trailing flag letters.
// Bails at a newline - a regex literal cannot span lines in real JS - by
// returning `start + 1` unchanged, so a false-positive `/` cannot run away
// through the rest of the file; the caller then treats it as ordinary code.
function scanRegexLiteral(src: string, start: number): number {
  let i = start + 1
  let inClass = false
  while (i < src.length) {
    const c = src[i]
    if (c === '\n') return start + 1
    if (c === '\\') {
      i += 2
      continue
    }
    if (c === '[') {
      inClass = true
      i++
      continue
    }
    if (c === ']') {
      inClass = false
      i++
      continue
    }
    if (c === '/' && !inClass) {
      i++
      while (i < src.length && /[a-z]/i.test(src[i])) i++
      return i
    }
    i++
  }
  return start + 1
}

export function withoutComments(src: string): string {
  let out = ''
  const stack: Frame[] = [{ mode: 'code' }]
  let i = 0
  let lastSignificant = ''

  while (i < src.length) {
    const frame = stack[stack.length - 1]
    const ch = src[i]

    if (frame.mode === 'code') {
      if (ch === '/' && src[i + 1] === '/') {
        // A line whose only content before the comment is whitespace has
        // that whitespace dropped too, so a comment-only line reads as
        // fully blank rather than leaving a bare indent behind.
        const lastNL = out.lastIndexOf('\n')
        const linePrefix = out.slice(lastNL + 1)
        if (/^\s*$/.test(linePrefix)) out = out.slice(0, lastNL + 1)
        stack.push({ mode: 'line-comment' })
        i += 2
        continue
      }
      if (ch === '/' && src[i + 1] === '*') {
        stack.push({ mode: 'block-comment' })
        i += 2
        continue
      }
      if (ch === '/' && REGEX_PRECEDING.has(lastSignificant)) {
        const end = scanRegexLiteral(src, i)
        out += src.slice(i, end)
        lastSignificant = src[end - 1]
        i = end
        continue
      }
      if (ch === "'") {
        stack.push({ mode: 'single' })
        out += ch
        i++
        continue
      }
      if (ch === '"') {
        stack.push({ mode: 'double' })
        out += ch
        i++
        continue
      }
      if (ch === '`') {
        stack.push({ mode: 'template' })
        out += ch
        i++
        continue
      }
      if (ch === '{' && frame.braceDepth !== undefined) {
        frame.braceDepth++
        out += ch
        lastSignificant = ch
        i++
        continue
      }
      if (ch === '}' && frame.braceDepth !== undefined) {
        frame.braceDepth--
        out += ch
        i++
        if (frame.braceDepth === 0) stack.pop()
        lastSignificant = '}'
        continue
      }
      out += ch
      if (ch === '\n') lastSignificant = '\n'
      else if (!/\s/.test(ch)) lastSignificant = ch
      i++
      continue
    }

    if (frame.mode === 'line-comment') {
      if (ch === '\n') {
        out += '\n'
        stack.pop()
        lastSignificant = '\n'
        i++
        continue
      }
      i++
      continue
    }

    if (frame.mode === 'block-comment') {
      if (ch === '*' && src[i + 1] === '/') {
        stack.pop()
        i += 2
        continue
      }
      if (ch === '\n') out += '\n'
      i++
      continue
    }

    if (frame.mode === 'single' || frame.mode === 'double') {
      const quote = frame.mode === 'single' ? "'" : '"'
      if (ch === '\\') {
        out += ch
        if (i + 1 < src.length) out += src[i + 1]
        i += 2
        continue
      }
      if (ch === quote) {
        stack.pop()
        out += ch
        lastSignificant = quote
        i++
        continue
      }
      if (ch === '\n') {
        // A raw, unescaped newline ends a single/double-quoted string in
        // real JS; falling back to CODE here matches that, so a runaway
        // string cannot swallow the rest of the file.
        stack.pop()
        out += ch
        lastSignificant = '\n'
        i++
        continue
      }
      out += ch
      i++
      continue
    }

    // frame.mode === 'template'
    if (ch === '\\') {
      out += ch
      if (i + 1 < src.length) out += src[i + 1]
      i += 2
      continue
    }
    if (ch === '`') {
      stack.pop()
      out += ch
      lastSignificant = '`'
      i++
      continue
    }
    if (ch === '$' && src[i + 1] === '{') {
      out += '${'
      stack.push({ mode: 'code', braceDepth: 1 })
      lastSignificant = '{'
      i += 2
      continue
    }
    out += ch
    i++
  }

  return out
}
