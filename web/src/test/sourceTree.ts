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
// JSX comments are `/* ... */` blocks underneath. Block comments are stripped
// first (they can span multiple lines), then `//` line comments on what
// remains.
//
// String literals are blanked before the block-comment pass runs, keeping
// their quotes and any embedded newlines: a `/*` inside a string (an
// accept-attribute value like 'image/*' is the shape that surfaced this)
// otherwise reads as an unterminated block comment and swallows every
// producer between it and the next real `*/`, or to EOF if there is none.
//
// KNOWN LIMITS. A `//` comment that TRAILS code on the same line is not
// stripped, only a line whose first non-space characters are the slashes. A
// template literal's `${...}` interpolation is not tokenised, so a stray `/*`
// or backtick inside an interpolated expression is not protected the way one
// inside a plain string is. And the line-comment pass itself is not
// string-aware: a line whose ENTIRE trimmed content is literal text starting
// with `//` inside a multi-line template literal would still be incorrectly
// stripped.
function blankStringLiterals(src: string): string {
  return src.replace(
    /'(?:\\.|[^'\\\n])*'|"(?:\\.|[^"\\\n])*"|`(?:\\.|[^`\\])*`/g,
    (m) => m[0] + m.slice(1, -1).replace(/[^\n]/g, ' ') + m[m.length - 1],
  )
}

export function withoutComments(src: string): string {
  // Block-comment boundaries are located on the string-blanked copy (same
  // length and newline positions as src, so match indices line up), and the
  // matched ranges are then removed from the REAL src - so a comment inside a
  // string is never mistaken for a delimiter, and the returned text still
  // holds real string content, not blanks.
  const blanked = blankStringLiterals(src)
  const blockComment = /\/\*[\s\S]*?\*\//g
  let noBlocks = ''
  let cursor = 0
  let match: RegExpExecArray | null
  while ((match = blockComment.exec(blanked))) {
    noBlocks += src.slice(cursor, match.index)
    cursor = match.index + match[0].length
  }
  noBlocks += src.slice(cursor)

  return noBlocks
    .split('\n')
    .map((line) => (line.trim().startsWith('//') ? '' : line))
    .join('\n')
}
