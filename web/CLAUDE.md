# web/ - Frontend Notes

**Playwright `isVisible()` cannot see clipping inside a scroll container.** It is `checkVisibility`
plus a non-empty box, so a link scrolled out of an `overflow-x-auto` wrapper still reports visible; a
reachability predicate built on it was green against the header-nav clipping bug it existed to catch.
A claim that a control is reachable needs `toBeInViewport()` (which clips against intermediate
scrollers) or a `scrollWidth <= clientWidth` assertion on the scroller, and a plan-supplied "fails at
HEAD" claim is measured by putting the pre-fix file back and running the test.

**Tailwind v4 scans the whole project, so prose is compiled input.** `@tailwindcss/vite` builds its
scanner over the Vite root with `pattern: '**/*'` and reads **source files on disk**, not the emitted
bundle - so a class-shaped substring anywhere under `web/`, including inside a comment or a test file,
emits CSS. Demonstrated 2026-08-24: a bracket-form placeholder written in a comment in
`web/e2e/keyboard.spec.ts` shipped a literal, invalid rule into the production stylesheet, and it
disappeared from the built CSS the moment the comment was reworded. So **prose that needs to name an
arbitrary-value class should spell it in a form the scanner does not match** - write the CSS property,
not the bracket form.

Two corollaries, both of which were got wrong before being measured. Because the scanner reads source
and never the bundle, **esbuild constant-folding a computed class back to a literal does not make it
visible to Tailwind** - a folded string is still absent from the CSS. And for the same reason a
template literal that interpolates the value inside the brackets is not a match either, so it neither
emits a rule nor rescues one; only a literal class-shaped substring counts. If a mutation that removes a class appears
not to reproduce, suspect a stale `web/dist` embed before suspecting the scanner - `//go:embed`
snapshots `web/dist` at compile time, so a Go binary built without a fresh `make web-build` serves the
previous bundle.
