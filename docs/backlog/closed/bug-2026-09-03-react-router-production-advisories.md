---
title: react-router 7.16.0 ships five advisories in the production bundle, one a high open redirect
type: bug
status: closed
created: 2026-09-03
closed: 2026-09-04
resolution: fixed
priority: high
source: fan-in of the 2026-09-02 web-frontend batch
---

# react-router 7.16.0 ships five advisories in the production bundle, one a high open redirect

## Summary
npm audit at lane T's HEAD reported react-router and react-router-dom 7.16.0 carrying five advisories that reach the production bundle, including GHSA-wrjc-x8rr-h8h6 (open redirect, high). The fix versions are inside the package.json ^7.1.1 range, so this is a lockfile bump with no API change. Lane T (PR #177) was scoped to vite, vitest and plugin-react and left it.

## Context
The item lane T closed had inherited an audit count of 5 that nobody re-measured for three months; the count at HEAD was 11, six of them unrelated to the tooling bump.

## Proposal
npm update react-router react-router-dom, confirm the advisories clear, run the SPA and e2e suites.

## Related
- web/package.json, web/package-lock.json

## Resolution
Fixed by a lockfile bump to react-router / react-router-dom 7.18.3 (inside the existing `^7.1.1`
range, no package.json edit). Before the bump `npm audit` reported three affected packages;
after it, only `undici`, which `--omit=dev` shows is absent from the production dependency tree
and which nothing under `web/src` imports (its only dependent is jsdom).

**On the counts, and on the axis.** npm's `metadata.total` counts affected PACKAGES, not
advisories, and every number in this item's history is on one axis or the other. Before the bump:
three packages (`react-router` high, `react-router-dom` moderate, `undici` high), with
`react-router`'s own `via` array holding five advisories. That is where this item's "five" comes
from - a per-package figure, not an audit total. **The roadmap's "11" was not a miscount.** It
was lane T's own correct measurement at its HEAD on the affected-packages axis, and lane T's
toolchain bump cleared eight of them (esbuild, vite, vitest, @vitest/mocker, vite-node,
browserslist, nanoid, postcss), leaving exactly these three. What is wrong in both records is the
WORD: they say "advisories" where npm counted packages.

**On the severities.** GHSA-wrjc-x8rr-h8h6, which this item's title calls "a high open redirect",
is rated MODERATE. `react-router`'s two HIGH advisories are GHSA-chx6-hx7r-mcp5 (denial of
service via inefficient route matching) and GHSA-qwww-vcr4-c8h2 (RSC mode CSRF bypass). Note the
scope of that sentence: it is about `react-router` alone. Across the whole pre-bump audit there
were six highs, because `undici` carried four more, and the "high 2" in npm's summary is the
package rollup (`react-router` and `undici`), not this pair.

**On reachability, which is what actually determines whether any of this mattered.** Two facts
pinned by files, rather than a search for absent symbols:

- `web/package.json` pins react and react-dom at `^18.3.1`, and react-router declares
  `peerDependencies: {react: ">=18"}`. RSC mode needs React 19 plus a `react-server-dom-*`
  runtime, so the two RSC advisories are structurally uninstallable here, not merely un-imported.
  One of them is one of the two highs.
- `web/index.html` is an empty `<div id="root">` with a single module script. There is no
  hydration payload for the SSR `deserializeErrors` advisory to consume.

That leaves the moderate open redirect and the route-matching DoS. Neither had a live sink. Every
navigation target in the SPA is a string literal or a template with a literal path prefix and a
server-supplied id, and the two nullable ones are guarded, so no `to` or `navigate()` value is
attacker-controlled. And relay-server does no react-router matching at all: `internal/api`
routes on a plain `http.NewServeMux`, and `web/embed.go` prefix-matches `/v1/` then falls back to
`index.html`. The DoS is a lured victim's own tab, not relay-server - the attacker picks the URL,
so it is not self-inflicted, but it has no server-side consequence.

So the bump is hygiene, correctly done, and not the urgent high-severity fix the item describes.

**Verified:** 189 test files / 1504 tests passed against the bumped version, `tsc -b` clean,
`npm run build` succeeded, Playwright 88 passed (51.6s) across chromium and webkit under the e2e
lock with zero skips. What that green does NOT observe: `BrowserRouter` appears only in
`App.tsx` and 59 test files use `MemoryRouter`, so the unit suite never constructs a browser
history and never exercises the URL normalization changed in 7.18.0. The suite bounds this bump
to "the happy paths still work"; it does not retire the normalization change.
