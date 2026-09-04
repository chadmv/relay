# `?q=` cost measurement

Produced by `go run ./scripts/qcost`. Every row carries its own input; a
measurement without its input reads as the typical case.

## Inputs, shared by every row

- `jobs` rows: **200000**
- `users` rows: **10000**
- sort arm: `-created_at` (`ListJobsWithEmailPage`, the default)
- page limit: **50**
- other filters on the request: **none** (no `status`, no `scheduled_job_id`, no `mine`, no `since`, no `until`)
- executions per statement: **20**
- Postgres: `PostgreSQL 16.13 (Debian 16.13-1.pgdg13+1) on x86_64-pc-linux-gnu, compiled by gcc (Debian 14.2.0-19) 14.2.0, 64-bit`
- statement timing only: this is the database's wall time for the statement, NOT a whole HTTP request

## Results

| case | statement | needle | rows matched | min | median | max |
|---|---|---|---:|---:|---:|---:|
| unfiltered | `CountJobs` | `(none)` | 200000 | 6.53 ms | 7.67 ms | 10.26 ms |
| unfiltered | `ListJobsWithEmailPage` | `(none)` | 200000 | 0.50 ms | 0.83 ms | 3.72 ms |
| no-match needle | `CountJobsWithText` | `zqxjvk-matches-nothing` | 0 | 41.02 ms | 42.57 ms | 43.93 ms |
| no-match needle | `ListJobsWithEmailPage` | `zqxjvk-matches-nothing` | 0 | 258.04 ms | 261.04 ms | 266.44 ms |
| matching needle | `CountJobsWithText` | `shotword007` | 2000 | 43.44 ms | 45.06 ms | 53.55 ms |
| matching needle | `ListJobsWithEmailPage` | `shotword007` | 2000 | 7.02 ms | 7.83 ms | 8.87 ms |

## What these numbers establish

**The amplifier is real and this slice does not reduce it.** The no-match needle costs 258-266 ms
on the list statement against 0.50-3.72 ms unfiltered, which is the same order as the 283 ms the
backlog item recorded. Nothing here makes the scan cheaper: both controls bound how often the
scan can be asked for and how long one instance may run, and the numbers above are expected to be
unchanged by this change. A reader must not infer a speedup from this table.

**The worst case is the empty result, not the large one.** A needle matching 2,000 rows costs
7.02-8.87 ms on the list statement - an order of magnitude LESS than a needle matching nothing -
because the page limit fills early and the walk stops. The no-match needle pays the whole walk and
returns nothing. That is why `maxFilterQRunes` bounds the wrong axis: it caps per-row work while
the dominant term is rows scanned, and the worst input is short.

**The count and the list are not the same shape.** For a no-match needle the list dominates
(258 ms against 41 ms); for a matching needle the count dominates (45 ms against 7 ms). The count
has no `LIMIT` and joins `users`, so it walks regardless; the list stops when the page fills. Any
future work on this cost has to move both.

## What they do not establish

- **These are statement times, not request times.** They are the database's wall time for one
  statement, measured on an otherwise idle box. A real request adds the pool wait, the handler,
  serialisation and the network, and under concurrency the pool wait is the term that grows.
- **They do not bound the fleet.** 200,000 rows and 10,000 users is one point. The relationship
  to table size was not measured, so nothing here supports extrapolating to a larger farm.
- **They say nothing about `GET /v1/scheduled-jobs`,** which shares the bucket. That table is
  small in every deployment seen so far, so it is charged at the jobs rate as a conservative
  error rather than a measured one.
- **The default was not derived from these numbers.** At 120 per 10 seconds a full window still
  exceeds one box at the measured cost, so the value is a fairness bound between principals, not
  a CPU budget. Its real constraint is relay's own client: the jobs view refetches five lanes
  every three seconds, each carrying the needle while one sits in the search box.
