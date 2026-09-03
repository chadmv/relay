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

// Strips comments before scanning, both kinds this repo writes. A previous
// version of this scan only stripped `//` line comments, and a real review lane
// broke it: inserting a prose comment into a fully compliant consumer made a
// tag-matching guard report it as a violation, because the regex stopped at the
// first `>` inside the comment's own prose. `{/* ... */}` JSX comments are
// `/* ... */` blocks underneath, and this repo writes them constantly, so a scan
// blind to them will eventually fire on someone documenting intent near the code
// a guard matches, and the fix people reach for under that pressure is to weaken
// the guard rather than the scan. Block comments are stripped first (they can
// span multiple lines), then `//` line comments on what remains.
//
// KNOWN LIMIT: a `//` comment that TRAILS code on the same line is not stripped,
// only a line whose first non-space characters are the slashes. No consumer
// writes one next to a class string today.
export function withoutComments(src: string): string {
  const noBlocks = src.replace(/\/\*[\s\S]*?\*\//g, '')
  return noBlocks
    .split('\n')
    .map((line) => (line.trim().startsWith('//') ? '' : line))
    .join('\n')
}
