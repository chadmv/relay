# EXPLAIN ANALYZE sort index verification

Generated: 2026-05-27T23:18:59Z
Postgres: 16.13 (Debian 16.13-1.pgdg13+1)
Result: 44/44 PASS

## Summary

| Table | Sort key | Dir | Index | Status | Notes |
| --- | --- | --- | --- | --- | --- |
| jobs | created_at | desc | `idx_jobs_created_id` | PASS |  |
| jobs | created_at | asc | `idx_jobs_created_id` | PASS |  |
| jobs | name | desc | `idx_jobs_name_id` | PASS |  |
| jobs | name | asc | `idx_jobs_name_id` | PASS |  |
| jobs | priority | desc | `idx_jobs_priority_id` | PASS |  |
| jobs | priority | asc | `idx_jobs_priority_id` | PASS |  |
| jobs | status | desc | `idx_jobs_status_id` | PASS |  |
| jobs | status | asc | `idx_jobs_status_id` | PASS |  |
| jobs | updated_at | desc | `idx_jobs_updated_id` | PASS |  |
| jobs | updated_at | asc | `idx_jobs_updated_id` | PASS |  |
| workers | created_at | desc | `idx_workers_created_id` | PASS |  |
| workers | created_at | asc | `idx_workers_created_id` | PASS |  |
| workers | last_seen_at | desc | `idx_workers_last_seen_desc` | PASS |  |
| workers | last_seen_at | asc | `idx_workers_last_seen_asc` | PASS |  |
| workers | name | desc | `idx_workers_name_id` | PASS |  |
| workers | name | asc | `idx_workers_name_id` | PASS |  |
| workers | status | desc | `idx_workers_status_id` | PASS |  |
| workers | status | asc | `idx_workers_status_id` | PASS |  |
| users | created_at | desc | `idx_users_created_id` | PASS |  |
| users | created_at | asc | `idx_users_created_id` | PASS |  |
| users | email | desc | `idx_users_email_id` | PASS |  |
| users | email | asc | `idx_users_email_id` | PASS |  |
| users | name | desc | `idx_users_name_id` | PASS |  |
| users | name | asc | `idx_users_name_id` | PASS |  |
| scheduled_jobs | created_at | desc | `idx_sched_jobs_created_id` | PASS |  |
| scheduled_jobs | created_at | asc | `idx_sched_jobs_created_id` | PASS |  |
| scheduled_jobs | name | desc | `idx_sched_jobs_name_id` | PASS |  |
| scheduled_jobs | name | asc | `idx_sched_jobs_name_id` | PASS |  |
| scheduled_jobs | next_run_at | desc | `idx_sched_jobs_next_run_id` | PASS |  |
| scheduled_jobs | next_run_at | asc | `idx_sched_jobs_next_run_id` | PASS |  |
| scheduled_jobs | updated_at | desc | `idx_sched_jobs_updated_id` | PASS |  |
| scheduled_jobs | updated_at | asc | `idx_sched_jobs_updated_id` | PASS |  |
| reservations | created_at | desc | `idx_reservations_created_id` | PASS |  |
| reservations | created_at | asc | `idx_reservations_created_id` | PASS |  |
| reservations | ends_at | desc | `idx_reservations_ends_desc` | PASS |  |
| reservations | ends_at | asc | `idx_reservations_ends_asc` | PASS |  |
| reservations | name | desc | `idx_reservations_name_id` | PASS |  |
| reservations | name | asc | `idx_reservations_name_id` | PASS |  |
| reservations | starts_at | desc | `idx_reservations_starts_desc` | PASS |  |
| reservations | starts_at | asc | `idx_reservations_starts_asc` | PASS |  |
| agent_enrollments | created_at | desc | `idx_agent_enr_created_id` | PASS |  |
| agent_enrollments | created_at | asc | `idx_agent_enr_created_id` | PASS |  |
| agent_enrollments | expires_at | desc | `idx_agent_enr_expires_id` | PASS |  |
| agent_enrollments | expires_at | asc | `idx_agent_enr_expires_id` | PASS |  |

## Plans

### jobs · created_at · desc

Index: `idx_jobs_created_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.71..7.02 rows=50 width=234) (actual time=0.015..0.145 rows=50 loops=1)
  Buffers: shared hit=185
  ->  Nested Loop  (cost=0.71..12615.54 rows=100000 width=234) (actual time=0.015..0.141 rows=50 loops=1)
        Buffers: shared hit=185
        ->  Index Scan using idx_jobs_created_id on jobs j  (cost=0.42..10052.41 rows=100000 width=100) (actual time=0.008..0.048 rows=50 loops=1)
              Buffers: shared hit=53
        ->  Memoize  (cost=0.30..0.32 rows=1 width=134) (actual time=0.002..0.002 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 6  Misses: 44  Evictions: 0  Overflows: 0  Memory Usage: 11kB
              Buffers: shared hit=132
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.31 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=44)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=132
Planning:
  Buffers: shared hit=16
Planning Time: 0.147 ms
Execution Time: 0.159 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.71..10.23 rows=50 width=234) (actual time=0.015..0.129 rows=50 loops=1)
  Buffers: shared hit=192
  ->  Nested Loop  (cost=0.71..9555.27 rows=50193 width=234) (actual time=0.015..0.126 rows=50 loops=1)
        Buffers: shared hit=192
        ->  Index Scan using idx_jobs_created_id on jobs j  (cost=0.42..8234.79 rows=50193 width=100) (actual time=0.009..0.046 rows=50 loops=1)
              Index Cond: (ROW(created_at, id) < ROW('2026-04-12 20:05:17.221302+00'::timestamp with time zone, 'cd331b44-4207-41a8-8812-e68882901a6f'::uuid))
              Buffers: shared hit=54
        ->  Memoize  (cost=0.30..0.34 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 4  Misses: 46  Evictions: 0  Overflows: 0  Memory Usage: 11kB
              Buffers: shared hit=138
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.33 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=46)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=138
Planning:
  Buffers: shared hit=4
Planning Time: 0.142 ms
Execution Time: 0.153 ms
```

</details>

### jobs · created_at · asc

Index: `idx_jobs_created_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.71..7.02 rows=50 width=234) (actual time=0.011..0.116 rows=50 loops=1)
  Buffers: shared hit=176
  ->  Nested Loop  (cost=0.71..12615.54 rows=100000 width=234) (actual time=0.011..0.113 rows=50 loops=1)
        Buffers: shared hit=176
        ->  Index Scan Backward using idx_jobs_created_id on jobs j  (cost=0.42..10052.41 rows=100000 width=100) (actual time=0.005..0.040 rows=50 loops=1)
              Buffers: shared hit=53
        ->  Memoize  (cost=0.30..0.32 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 9  Misses: 41  Evictions: 0  Overflows: 0  Memory Usage: 10kB
              Buffers: shared hit=123
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.31 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=41)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=123
Planning:
  Buffers: shared hit=4
Planning Time: 0.096 ms
Execution Time: 0.128 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.71..10.28 rows=50 width=234) (actual time=0.013..0.125 rows=50 loops=1)
  Buffers: shared hit=182
  ->  Nested Loop  (cost=0.71..9526.86 rows=49806 width=234) (actual time=0.013..0.122 rows=50 loops=1)
        Buffers: shared hit=182
        ->  Index Scan Backward using idx_jobs_created_id on jobs j  (cost=0.42..8216.02 rows=49806 width=100) (actual time=0.008..0.041 rows=50 loops=1)
              Index Cond: (ROW(created_at, id) > ROW('2026-04-12 20:05:23.912531+00'::timestamp with time zone, '4209c8a2-a7a5-4bb0-90e3-b968e747c79c'::uuid))
              Buffers: shared hit=53
        ->  Memoize  (cost=0.30..0.34 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 7  Misses: 43  Evictions: 0  Overflows: 0  Memory Usage: 11kB
              Buffers: shared hit=129
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.33 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=43)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=129
Planning:
  Buffers: shared hit=4
Planning Time: 0.100 ms
Execution Time: 0.141 ms
```

</details>

### jobs · name · desc

Index: `idx_jobs_name_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.71..7.63 rows=50 width=234) (actual time=0.010..0.111 rows=50 loops=1)
  Buffers: shared hit=182
  ->  Nested Loop  (cost=0.71..13843.52 rows=100000 width=234) (actual time=0.010..0.108 rows=50 loops=1)
        Buffers: shared hit=182
        ->  Index Scan using idx_jobs_name_id on jobs j  (cost=0.42..11280.40 rows=100000 width=100) (actual time=0.005..0.036 rows=50 loops=1)
              Buffers: shared hit=53
        ->  Memoize  (cost=0.30..0.32 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 7  Misses: 43  Evictions: 0  Overflows: 0  Memory Usage: 11kB
              Buffers: shared hit=129
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.31 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=43)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=129
Planning:
  Buffers: shared hit=4
Planning Time: 0.098 ms
Execution Time: 0.122 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.71..10.74 rows=50 width=234) (actual time=0.011..0.123 rows=50 loops=1)
  Buffers: shared hit=189
  ->  Nested Loop  (cost=0.71..10240.78 rows=51078 width=234) (actual time=0.011..0.121 rows=50 loops=1)
        Buffers: shared hit=189
        ->  Index Scan using idx_jobs_name_id on jobs j  (cost=0.42..8898.26 rows=51078 width=100) (actual time=0.007..0.050 rows=50 loops=1)
              Index Cond: (ROW(name, id) < ROW('migrate-asset-review'::text, '13edfdc6-7930-4c9c-8d68-999af371e713'::uuid))
              Buffers: shared hit=54
        ->  Memoize  (cost=0.30..0.34 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 5  Misses: 45  Evictions: 0  Overflows: 0  Memory Usage: 11kB
              Buffers: shared hit=135
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.33 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=45)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=135
Planning:
  Buffers: shared hit=4
Planning Time: 0.116 ms
Execution Time: 0.145 ms
```

</details>

### jobs · name · asc

Index: `idx_jobs_name_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.71..7.63 rows=50 width=234) (actual time=0.016..0.148 rows=50 loops=1)
  Buffers: shared hit=188
  ->  Nested Loop  (cost=0.71..13843.52 rows=100000 width=234) (actual time=0.016..0.144 rows=50 loops=1)
        Buffers: shared hit=188
        ->  Index Scan Backward using idx_jobs_name_id on jobs j  (cost=0.42..11280.40 rows=100000 width=100) (actual time=0.008..0.045 rows=50 loops=1)
              Buffers: shared hit=53
        ->  Memoize  (cost=0.30..0.32 rows=1 width=134) (actual time=0.002..0.002 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 5  Misses: 45  Evictions: 0  Overflows: 0  Memory Usage: 11kB
              Buffers: shared hit=135
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.31 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=45)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=135
Planning:
  Buffers: shared hit=4
Planning Time: 0.148 ms
Execution Time: 0.164 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.71..11.01 rows=50 width=234) (actual time=0.019..0.121 rows=50 loops=1)
  Buffers: shared hit=197
  ->  Nested Loop  (cost=0.71..10068.64 rows=48905 width=234) (actual time=0.019..0.118 rows=50 loops=1)
        Buffers: shared hit=197
        ->  Index Scan Backward using idx_jobs_name_id on jobs j  (cost=0.42..8780.23 rows=48905 width=100) (actual time=0.014..0.044 rows=50 loops=1)
              Index Cond: (ROW(name, id) > ROW('migrate-asset-review'::text, '15d3baa5-2def-4ffd-8eb9-859820a0ec73'::uuid))
              Buffers: shared hit=53
        ->  Memoize  (cost=0.30..0.34 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 2  Misses: 48  Evictions: 0  Overflows: 0  Memory Usage: 12kB
              Buffers: shared hit=144
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.33 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=48)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=144
Planning:
  Buffers: shared hit=4
Planning Time: 0.116 ms
Execution Time: 0.135 ms
```

</details>

### jobs · priority · desc

Index: `idx_jobs_priority_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.71..6.96 rows=50 width=234) (actual time=0.013..0.134 rows=50 loops=1)
  Buffers: shared hit=182
  ->  Nested Loop  (cost=0.71..12504.07 rows=100000 width=234) (actual time=0.013..0.131 rows=50 loops=1)
        Buffers: shared hit=182
        ->  Index Scan using idx_jobs_priority_id on jobs j  (cost=0.42..9940.95 rows=100000 width=100) (actual time=0.007..0.044 rows=50 loops=1)
              Buffers: shared hit=53
        ->  Memoize  (cost=0.30..0.32 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 7  Misses: 43  Evictions: 0  Overflows: 0  Memory Usage: 11kB
              Buffers: shared hit=129
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.31 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=43)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=129
Planning:
  Buffers: shared hit=4
Planning Time: 0.120 ms
Execution Time: 0.150 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.71..11.07 rows=50 width=234) (actual time=0.022..0.157 rows=50 loops=1)
  Buffers: shared hit=194
  ->  Nested Loop  (cost=0.71..8814.14 rows=42553 width=234) (actual time=0.022..0.154 rows=50 loops=1)
        Buffers: shared hit=194
        ->  Index Scan using idx_jobs_priority_id on jobs j  (cost=0.42..7683.76 rows=42553 width=100) (actual time=0.016..0.070 rows=50 loops=1)
              Index Cond: (ROW(priority, id) < ROW('low'::text, '81a17252-7e08-4af2-87fd-56703e051c10'::uuid))
              Buffers: shared hit=53
        ->  Memoize  (cost=0.30..0.34 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 3  Misses: 47  Evictions: 0  Overflows: 0  Memory Usage: 12kB
              Buffers: shared hit=141
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.33 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=47)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=141
Planning:
  Buffers: shared hit=4
Planning Time: 0.130 ms
Execution Time: 0.175 ms
```

</details>

### jobs · priority · asc

Index: `idx_jobs_priority_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.71..6.96 rows=50 width=234) (actual time=0.011..0.117 rows=50 loops=1)
  Buffers: shared hit=182
  ->  Nested Loop  (cost=0.71..12504.07 rows=100000 width=234) (actual time=0.011..0.114 rows=50 loops=1)
        Buffers: shared hit=182
        ->  Index Scan Backward using idx_jobs_priority_id on jobs j  (cost=0.42..9940.95 rows=100000 width=100) (actual time=0.006..0.041 rows=50 loops=1)
              Buffers: shared hit=53
        ->  Memoize  (cost=0.30..0.32 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 7  Misses: 43  Evictions: 0  Overflows: 0  Memory Usage: 11kB
              Buffers: shared hit=129
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.31 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=43)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=129
Planning:
  Buffers: shared hit=4
Planning Time: 0.092 ms
Execution Time: 0.129 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.71..10.98 rows=50 width=234) (actual time=0.013..0.151 rows=50 loops=1)
  Buffers: shared hit=193
  ->  Nested Loop  (cost=0.71..8853.31 rows=43090 width=234) (actual time=0.013..0.148 rows=50 loops=1)
        Buffers: shared hit=193
        ->  Index Scan Backward using idx_jobs_priority_id on jobs j  (cost=0.42..7709.57 rows=43090 width=100) (actual time=0.008..0.061 rows=50 loops=1)
              Index Cond: (ROW(priority, id) > ROW('low'::text, '81a68ff3-6ebd-4f83-b69e-fd3ea07693e8'::uuid))
              Buffers: shared hit=55
        ->  Memoize  (cost=0.30..0.34 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 4  Misses: 46  Evictions: 0  Overflows: 0  Memory Usage: 11kB
              Buffers: shared hit=138
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.33 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=46)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=138
Planning:
  Buffers: shared hit=4
Planning Time: 0.098 ms
Execution Time: 0.164 ms
```

</details>

### jobs · status · desc

Index: `idx_jobs_status_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.71..7.07 rows=50 width=234) (actual time=0.011..0.107 rows=50 loops=1)
  Buffers: shared hit=179
  ->  Nested Loop  (cost=0.71..12718.19 rows=100000 width=234) (actual time=0.011..0.104 rows=50 loops=1)
        Buffers: shared hit=179
        ->  Index Scan using idx_jobs_status_id on jobs j  (cost=0.42..10155.07 rows=100000 width=100) (actual time=0.006..0.038 rows=50 loops=1)
              Buffers: shared hit=53
        ->  Memoize  (cost=0.30..0.32 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 8  Misses: 42  Evictions: 0  Overflows: 0  Memory Usage: 10kB
              Buffers: shared hit=126
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.31 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=42)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=126
Planning:
  Buffers: shared hit=4
Planning Time: 0.094 ms
Execution Time: 0.118 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.71..19.25 rows=50 width=234) (actual time=0.012..0.106 rows=50 loops=1)
  Buffers: shared hit=183
  ->  Nested Loop  (cost=0.71..7432.75 rows=20050 width=234) (actual time=0.012..0.104 rows=50 loops=1)
        Buffers: shared hit=183
        ->  Index Scan using idx_jobs_status_id on jobs j  (cost=0.42..6858.39 rows=20050 width=100) (actual time=0.008..0.041 rows=50 loops=1)
              Index Cond: (ROW(status, id) < ROW('done'::text, 'bef01678-30c0-451d-bb2d-a0e6155d20fa'::uuid))
              Buffers: shared hit=54
        ->  Memoize  (cost=0.30..0.37 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 7  Misses: 43  Evictions: 0  Overflows: 0  Memory Usage: 11kB
              Buffers: shared hit=129
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.36 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=43)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=129
Planning:
  Buffers: shared hit=4
Planning Time: 0.102 ms
Execution Time: 0.118 ms
```

</details>

### jobs · status · asc

Index: `idx_jobs_status_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.71..7.07 rows=50 width=234) (actual time=0.011..0.105 rows=50 loops=1)
  Buffers: shared hit=191
  ->  Nested Loop  (cost=0.71..12718.19 rows=100000 width=234) (actual time=0.011..0.102 rows=50 loops=1)
        Buffers: shared hit=191
        ->  Index Scan Backward using idx_jobs_status_id on jobs j  (cost=0.42..10155.07 rows=100000 width=100) (actual time=0.006..0.035 rows=50 loops=1)
              Buffers: shared hit=53
        ->  Memoize  (cost=0.30..0.32 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 4  Misses: 46  Evictions: 0  Overflows: 0  Memory Usage: 11kB
              Buffers: shared hit=138
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.31 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=46)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=138
Planning:
  Buffers: shared hit=4
Planning Time: 0.090 ms
Execution Time: 0.115 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.71..11.79 rows=50 width=234) (actual time=0.011..0.092 rows=50 loops=1)
  Buffers: shared hit=167
  ->  Nested Loop  (cost=0.71..8788.91 rows=39663 width=234) (actual time=0.010..0.090 rows=50 loops=1)
        Buffers: shared hit=167
        ->  Index Scan Backward using idx_jobs_status_id on jobs j  (cost=0.42..7730.36 rows=39663 width=100) (actual time=0.006..0.034 rows=50 loops=1)
              Index Cond: (ROW(status, id) > ROW('done'::text, 'bef08282-e61d-4369-bdf5-1d84e1d3ecf6'::uuid))
              Buffers: shared hit=53
        ->  Memoize  (cost=0.30..0.34 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 12  Misses: 38  Evictions: 0  Overflows: 0  Memory Usage: 10kB
              Buffers: shared hit=114
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.33 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=38)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=114
Planning:
  Buffers: shared hit=4
Planning Time: 0.099 ms
Execution Time: 0.104 ms
```

</details>

### jobs · updated_at · desc

Index: `idx_jobs_updated_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.71..7.02 rows=50 width=234) (actual time=0.014..0.139 rows=50 loops=1)
  Buffers: shared hit=182
  ->  Nested Loop  (cost=0.71..12615.54 rows=100000 width=234) (actual time=0.014..0.135 rows=50 loops=1)
        Buffers: shared hit=182
        ->  Index Scan using idx_jobs_updated_id on jobs j  (cost=0.42..10052.41 rows=100000 width=100) (actual time=0.008..0.046 rows=50 loops=1)
              Buffers: shared hit=53
        ->  Memoize  (cost=0.30..0.32 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 7  Misses: 43  Evictions: 0  Overflows: 0  Memory Usage: 11kB
              Buffers: shared hit=129
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.31 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=43)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=129
Planning:
  Buffers: shared hit=4
Planning Time: 0.121 ms
Execution Time: 0.154 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.71..10.23 rows=50 width=234) (actual time=0.015..0.118 rows=50 loops=1)
  Buffers: shared hit=188
  ->  Nested Loop  (cost=0.71..9555.14 rows=50190 width=234) (actual time=0.015..0.116 rows=50 loops=1)
        Buffers: shared hit=188
        ->  Index Scan using idx_jobs_updated_id on jobs j  (cost=0.42..8234.74 rows=50190 width=100) (actual time=0.009..0.041 rows=50 loops=1)
              Index Cond: (ROW(updated_at, id) < ROW('2026-04-12 23:19:32.680584+00'::timestamp with time zone, '39066e4e-bf10-4d46-8aeb-8888e4fc5b44'::uuid))
              Buffers: shared hit=53
        ->  Memoize  (cost=0.30..0.34 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 5  Misses: 45  Evictions: 0  Overflows: 0  Memory Usage: 11kB
              Buffers: shared hit=135
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.33 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=45)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=135
Planning:
  Buffers: shared hit=4
Planning Time: 0.130 ms
Execution Time: 0.134 ms
```

</details>

### jobs · updated_at · asc

Index: `idx_jobs_updated_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.71..7.02 rows=50 width=234) (actual time=0.010..0.115 rows=50 loops=1)
  Buffers: shared hit=194
  ->  Nested Loop  (cost=0.71..12615.54 rows=100000 width=234) (actual time=0.010..0.112 rows=50 loops=1)
        Buffers: shared hit=194
        ->  Index Scan Backward using idx_jobs_updated_id on jobs j  (cost=0.42..10052.41 rows=100000 width=100) (actual time=0.005..0.037 rows=50 loops=1)
              Buffers: shared hit=53
        ->  Memoize  (cost=0.30..0.32 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 3  Misses: 47  Evictions: 0  Overflows: 0  Memory Usage: 12kB
              Buffers: shared hit=141
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.31 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=47)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=141
Planning:
  Buffers: shared hit=4
Planning Time: 0.089 ms
Execution Time: 0.127 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.71..10.28 rows=50 width=234) (actual time=0.010..0.111 rows=50 loops=1)
  Buffers: shared hit=181
  ->  Nested Loop  (cost=0.71..9526.94 rows=49808 width=234) (actual time=0.010..0.108 rows=50 loops=1)
        Buffers: shared hit=181
        ->  Index Scan Backward using idx_jobs_updated_id on jobs j  (cost=0.42..8216.05 rows=49808 width=100) (actual time=0.005..0.039 rows=50 loops=1)
              Index Cond: (ROW(updated_at, id) > ROW('2026-04-12 23:19:53.137301+00'::timestamp with time zone, '08a00626-1c8a-40ba-a00a-e0706c0c354d'::uuid))
              Buffers: shared hit=55
        ->  Memoize  (cost=0.30..0.34 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=50)
              Cache Key: j.submitted_by
              Cache Mode: logical
              Hits: 8  Misses: 42  Evictions: 0  Overflows: 0  Memory Usage: 10kB
              Buffers: shared hit=126
              ->  Index Scan using users_pkey on users u  (cost=0.29..0.33 rows=1 width=134) (actual time=0.001..0.001 rows=1 loops=42)
                    Index Cond: (id = j.submitted_by)
                    Buffers: shared hit=126
Planning:
  Buffers: shared hit=4
Planning Time: 0.099 ms
Execution Time: 0.124 ms
```

</details>

### workers · created_at · desc

Index: `idx_workers_created_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.31 rows=50 width=140) (actual time=0.005..0.037 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_workers_created_id on workers  (cost=0.29..1206.28 rows=10000 width=140) (actual time=0.005..0.034 rows=50 loops=1)
        Buffers: shared hit=52
Planning:
  Buffers: shared hit=28
Planning Time: 0.075 ms
Execution Time: 0.042 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..10.28 rows=50 width=140) (actual time=0.006..0.030 rows=50 loops=1)
  Buffers: shared hit=51
  ->  Index Scan using idx_workers_created_id on workers  (cost=0.29..999.76 rows=4999 width=140) (actual time=0.006..0.027 rows=50 loops=1)
        Index Cond: (ROW(created_at, id) < ROW('2026-02-26 05:15:42.091757+00'::timestamp with time zone, '19f5c4b6-9137-4c41-9afb-d44e2935856a'::uuid))
        Buffers: shared hit=51
Planning:
  Buffers: shared hit=1
Planning Time: 0.037 ms
Execution Time: 0.037 ms
```

</details>

### workers · created_at · asc

Index: `idx_workers_created_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.31 rows=50 width=140) (actual time=0.005..0.025 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_workers_created_id on workers  (cost=0.29..1206.28 rows=10000 width=140) (actual time=0.005..0.022 rows=50 loops=1)
        Buffers: shared hit=52
Planning:
  Buffers: shared hit=1
Planning Time: 0.030 ms
Execution Time: 0.043 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..10.28 rows=50 width=140) (actual time=0.004..0.027 rows=50 loops=1)
  Buffers: shared hit=53
  ->  Index Scan Backward using idx_workers_created_id on workers  (cost=0.29..999.78 rows=5000 width=140) (actual time=0.003..0.024 rows=50 loops=1)
        Index Cond: (ROW(created_at, id) > ROW('2026-02-26 05:19:06.538004+00'::timestamp with time zone, '262f05b0-9dc9-43db-a4cd-70937306a4d7'::uuid))
        Buffers: shared hit=53
Planning:
  Buffers: shared hit=1
Planning Time: 0.049 ms
Execution Time: 0.034 ms
```

</details>

### workers · last_seen_at · desc

Index: `idx_workers_last_seen_desc` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.33 rows=50 width=140) (actual time=0.017..0.036 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_workers_last_seen_asc on workers  (cost=0.29..1210.28 rows=10000 width=140) (actual time=0.005..0.022 rows=50 loops=1)
        Buffers: shared hit=52
Planning:
  Buffers: shared hit=1
Planning Time: 0.029 ms
Execution Time: 0.042 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..13.53 rows=50 width=140) (actual time=0.009..0.039 rows=50 loops=1)
  Buffers: shared hit=54
  ->  Index Scan Backward using idx_workers_last_seen_asc on workers  (cost=0.29..933.97 rows=3525 width=140) (actual time=0.008..0.036 rows=50 loops=1)
        Index Cond: (ROW(last_seen_at, id) < ROW('2026-05-13 06:59:08.276133+00'::timestamp with time zone, '3603ba9e-37ec-44b9-a74d-7978729275ad'::uuid))
        Buffers: shared hit=54
Planning:
  Buffers: shared hit=1
Planning Time: 0.058 ms
Execution Time: 0.049 ms
```

</details>

### workers · last_seen_at · asc

Index: `idx_workers_last_seen_asc` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.33 rows=50 width=140) (actual time=0.006..0.042 rows=50 loops=1)
  Buffers: shared hit=51
  ->  Index Scan using idx_workers_last_seen_asc on workers  (cost=0.29..1210.28 rows=10000 width=140) (actual time=0.006..0.039 rows=50 loops=1)
        Buffers: shared hit=51
Planning:
  Buffers: shared hit=1
Planning Time: 0.037 ms
Execution Time: 0.049 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..13.54 rows=50 width=140) (actual time=0.007..0.032 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_workers_last_seen_asc on workers  (cost=0.29..933.92 rows=3522 width=140) (actual time=0.006..0.029 rows=50 loops=1)
        Index Cond: (ROW(last_seen_at, id) > ROW('2026-05-13 07:22:50.636302+00'::timestamp with time zone, '794dba64-eec9-4f69-bdae-5abf712c7f1d'::uuid))
        Buffers: shared hit=52
Planning:
  Buffers: shared hit=1
Planning Time: 0.043 ms
Execution Time: 0.040 ms
```

</details>

### workers · name · desc

Index: `idx_workers_name_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.33 rows=50 width=140) (actual time=0.007..0.029 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_workers_name_id on workers  (cost=0.29..1210.28 rows=10000 width=140) (actual time=0.006..0.027 rows=50 loops=1)
        Buffers: shared hit=52
Planning:
  Buffers: shared hit=1
Planning Time: 0.031 ms
Execution Time: 0.035 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..10.43 rows=50 width=140) (actual time=0.007..0.039 rows=50 loops=1)
  Buffers: shared hit=53
  ->  Index Scan using idx_workers_name_id on workers  (cost=0.29..1002.74 rows=4941 width=140) (actual time=0.007..0.036 rows=50 loops=1)
        Index Cond: (ROW(name, id) < ROW('James'::text, '40a50095-44ad-41bf-8562-9816b7cd534a'::uuid))
        Buffers: shared hit=53
Planning:
  Buffers: shared hit=1
Planning Time: 0.065 ms
Execution Time: 0.047 ms
```

</details>

### workers · name · asc

Index: `idx_workers_name_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.33 rows=50 width=140) (actual time=0.007..0.032 rows=50 loops=1)
  Buffers: shared hit=51
  ->  Index Scan Backward using idx_workers_name_id on workers  (cost=0.29..1210.28 rows=10000 width=140) (actual time=0.006..0.029 rows=50 loops=1)
        Buffers: shared hit=51
Planning:
  Buffers: shared hit=1
Planning Time: 0.036 ms
Execution Time: 0.039 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..10.39 rows=50 width=140) (actual time=0.010..0.034 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_workers_name_id on workers  (cost=0.29..1003.08 rows=4960 width=140) (actual time=0.010..0.031 rows=50 loops=1)
        Index Cond: (ROW(name, id) > ROW('James'::text, '4645688f-4b0d-4fc9-9e22-c2ea812bf6ec'::uuid))
        Buffers: shared hit=52
Planning:
  Buffers: shared hit=1
Planning Time: 0.062 ms
Execution Time: 0.041 ms
```

</details>

### workers · status · desc

Index: `idx_workers_status_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.09 rows=50 width=140) (actual time=0.005..0.027 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_workers_status_id on workers  (cost=0.29..1161.66 rows=10000 width=140) (actual time=0.005..0.024 rows=50 loops=1)
        Buffers: shared hit=52
Planning:
  Buffers: shared hit=1
Planning Time: 0.029 ms
Execution Time: 0.045 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..16.65 rows=50 width=140) (actual time=0.007..0.030 rows=50 loops=1)
  Buffers: shared hit=53
  ->  Index Scan using idx_workers_status_id on workers  (cost=0.29..833.06 rows=2545 width=140) (actual time=0.006..0.027 rows=50 loops=1)
        Index Cond: (ROW(status, id) < ROW('offline'::text, '7cd91584-b8d0-4db7-81fe-f973c5e6ef7e'::uuid))
        Buffers: shared hit=53
Planning:
  Buffers: shared hit=1
Planning Time: 0.049 ms
Execution Time: 0.038 ms
```

</details>

### workers · status · asc

Index: `idx_workers_status_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.09 rows=50 width=140) (actual time=0.008..0.041 rows=50 loops=1)
  Buffers: shared hit=51
  ->  Index Scan Backward using idx_workers_status_id on workers  (cost=0.29..1161.66 rows=10000 width=140) (actual time=0.008..0.038 rows=50 loops=1)
        Buffers: shared hit=51
Planning:
  Buffers: shared hit=1
Planning Time: 0.052 ms
Execution Time: 0.050 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..17.25 rows=50 width=140) (actual time=0.009..0.032 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_workers_status_id on workers  (cost=0.29..827.01 rows=2437 width=140) (actual time=0.009..0.029 rows=50 loops=1)
        Index Cond: (ROW(status, id) > ROW('offline'::text, '7cdb19d2-fb01-4421-a64c-f54fdd7ce64c'::uuid))
        Buffers: shared hit=52
Planning:
  Buffers: shared hit=1
Planning Time: 0.044 ms
Execution Time: 0.041 ms
```

</details>

### users · created_at · desc

Index: `idx_users_created_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..5.40 rows=50 width=134) (actual time=0.008..0.043 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_users_created_id on users  (cost=0.29..1023.97 rows=10000 width=134) (actual time=0.008..0.040 rows=50 loops=1)
        Filter: (archived_at IS NULL)
        Buffers: shared hit=52
Planning Time: 0.036 ms
Execution Time: 0.048 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..6.05 rows=1 width=134) (actual time=0.007..0.041 rows=50 loops=1)
  Buffers: shared hit=53
  ->  Index Scan using idx_users_created_id on users  (cost=0.29..6.05 rows=1 width=134) (actual time=0.006..0.039 rows=50 loops=1)
        Index Cond: (ROW(created_at, id) < ROW('2026-05-27 23:18:55.068615+00'::timestamp with time zone, '802ec881-2feb-4778-86ca-8dde7d190bc1'::uuid))
        Filter: (archived_at IS NULL)
        Buffers: shared hit=53
Planning Time: 0.051 ms
Execution Time: 0.049 ms
```

</details>

### users · created_at · asc

Index: `idx_users_created_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..5.40 rows=50 width=134) (actual time=0.006..0.032 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_users_created_id on users  (cost=0.29..1023.97 rows=10000 width=134) (actual time=0.006..0.029 rows=50 loops=1)
        Filter: (archived_at IS NULL)
        Buffers: shared hit=52
Planning Time: 0.028 ms
Execution Time: 0.050 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..6.05 rows=1 width=134) (actual time=0.011..0.043 rows=50 loops=1)
  Buffers: shared hit=51
  ->  Index Scan Backward using idx_users_created_id on users  (cost=0.29..6.05 rows=1 width=134) (actual time=0.010..0.040 rows=50 loops=1)
        Index Cond: (ROW(created_at, id) > ROW('2026-05-27 23:18:55.068615+00'::timestamp with time zone, '80496d4c-3a45-4765-b957-bc57a8495095'::uuid))
        Filter: (archived_at IS NULL)
        Buffers: shared hit=51
Planning Time: 0.051 ms
Execution Time: 0.053 ms
```

</details>

### users · email · desc

Index: `idx_users_email_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..8.65 rows=50 width=134) (actual time=0.007..0.032 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_users_email_id on users  (cost=0.29..1674.15 rows=10000 width=134) (actual time=0.006..0.030 rows=50 loops=1)
        Filter: (archived_at IS NULL)
        Buffers: shared hit=52
Planning Time: 0.048 ms
Execution Time: 0.039 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..13.92 rows=50 width=134) (actual time=0.012..0.038 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_users_email_id on users  (cost=0.29..1363.61 rows=4999 width=134) (actual time=0.012..0.036 rows=50 loops=1)
        Index Cond: (ROW(email, id) < ROW('user-81ade1afe75ca705@example.com'::text, 'caf766d8-ba4c-4347-975c-2bfbeed71184'::uuid))
        Filter: (archived_at IS NULL)
        Buffers: shared hit=52
Planning:
  Buffers: shared hit=9
Planning Time: 0.078 ms
Execution Time: 0.045 ms
```

</details>

### users · email · asc

Index: `idx_users_email_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..8.65 rows=50 width=134) (actual time=0.007..0.030 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_users_email_id on users  (cost=0.29..1674.15 rows=10000 width=134) (actual time=0.006..0.027 rows=50 loops=1)
        Filter: (archived_at IS NULL)
        Buffers: shared hit=52
Planning Time: 0.029 ms
Execution Time: 0.048 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..13.92 rows=50 width=134) (actual time=0.011..0.060 rows=50 loops=1)
  Buffers: shared hit=54
  ->  Index Scan Backward using idx_users_email_id on users  (cost=0.29..1363.59 rows=4998 width=134) (actual time=0.011..0.057 rows=50 loops=1)
        Index Cond: (ROW(email, id) > ROW('user-81b42ae16e732488@example.com'::text, '5a086d82-98e0-4312-83cc-7b924cbbc683'::uuid))
        Filter: (archived_at IS NULL)
        Buffers: shared hit=54
Planning:
  Buffers: shared hit=9
Planning Time: 0.136 ms
Execution Time: 0.069 ms
```

</details>

### users · name · desc

Index: `idx_users_name_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..7.55 rows=50 width=134) (actual time=0.007..0.032 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_users_name_id on users  (cost=0.29..1454.28 rows=10000 width=134) (actual time=0.007..0.029 rows=50 loops=1)
        Filter: (archived_at IS NULL)
        Buffers: shared hit=52
Planning Time: 0.036 ms
Execution Time: 0.039 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..13.11 rows=50 width=134) (actual time=0.049..0.086 rows=50 loops=1)
  Buffers: shared hit=51
  ->  Index Scan using idx_users_name_id on users  (cost=0.29..1249.49 rows=4869 width=134) (actual time=0.049..0.083 rows=50 loops=1)
        Index Cond: (ROW(name, id) < ROW('Jason'::text, 'e019ba72-a89e-4741-98f1-60c26013e037'::uuid))
        Filter: (archived_at IS NULL)
        Buffers: shared hit=51
Planning Time: 0.279 ms
Execution Time: 0.095 ms
```

</details>

### users · name · asc

Index: `idx_users_name_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..7.55 rows=50 width=134) (actual time=0.011..0.046 rows=50 loops=1)
  Buffers: shared hit=50
  ->  Index Scan Backward using idx_users_name_id on users  (cost=0.29..1454.28 rows=10000 width=134) (actual time=0.011..0.044 rows=50 loops=1)
        Filter: (archived_at IS NULL)
        Buffers: shared hit=50
Planning Time: 0.063 ms
Execution Time: 0.054 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..12.75 rows=50 width=134) (actual time=0.063..0.124 rows=50 loops=1)
  Buffers: shared hit=54
  ->  Index Scan Backward using idx_users_name_id on users  (cost=0.29..1256.43 rows=5037 width=134) (actual time=0.062..0.121 rows=50 loops=1)
        Index Cond: (ROW(name, id) > ROW('Jason'::text, 'e3eda6ac-cd01-439d-b84c-0773e4c9412e'::uuid))
        Filter: (archived_at IS NULL)
        Buffers: shared hit=54
Planning Time: 0.058 ms
Execution Time: 0.132 ms
```

</details>

### scheduled_jobs · created_at · desc

Index: `idx_sched_jobs_created_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..7.53 rows=50 width=147) (actual time=0.006..0.037 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_sched_jobs_created_id on scheduled_jobs  (cost=0.29..1450.25 rows=10000 width=147) (actual time=0.006..0.034 rows=50 loops=1)
        Buffers: shared hit=52
Planning:
  Buffers: shared hit=21
Planning Time: 0.065 ms
Execution Time: 0.043 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..12.80 rows=50 width=147) (actual time=0.009..0.034 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_sched_jobs_created_id on scheduled_jobs  (cost=0.29..1251.73 rows=4999 width=147) (actual time=0.009..0.031 rows=50 loops=1)
        Index Cond: (ROW(created_at, id) < ROW('2026-05-12 23:19:55.620566+00'::timestamp with time zone, 'db974b5e-d7a9-46c8-8714-8f0a39686cd2'::uuid))
        Buffers: shared hit=52
Planning Time: 0.048 ms
Execution Time: 0.042 ms
```

</details>

### scheduled_jobs · created_at · asc

Index: `idx_sched_jobs_created_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..7.53 rows=50 width=147) (actual time=0.005..0.028 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_sched_jobs_created_id on scheduled_jobs  (cost=0.29..1450.25 rows=10000 width=147) (actual time=0.005..0.025 rows=50 loops=1)
        Buffers: shared hit=52
Planning Time: 0.025 ms
Execution Time: 0.046 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..12.80 rows=50 width=147) (actual time=0.005..0.026 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_sched_jobs_created_id on scheduled_jobs  (cost=0.29..1251.74 rows=5000 width=147) (actual time=0.005..0.023 rows=50 loops=1)
        Index Cond: (ROW(created_at, id) > ROW('2026-05-12 23:21:10.362483+00'::timestamp with time zone, '8e4145af-d76c-4275-80ba-f2dc2c3c4f3e'::uuid))
        Buffers: shared hit=52
Planning Time: 0.052 ms
Execution Time: 0.033 ms
```

</details>

### scheduled_jobs · name · desc

Index: `idx_sched_jobs_name_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..8.15 rows=50 width=147) (actual time=0.006..0.027 rows=50 loops=1)
  Buffers: shared hit=51
  ->  Index Scan using idx_sched_jobs_name_id on scheduled_jobs  (cost=0.29..1574.23 rows=10000 width=147) (actual time=0.006..0.024 rows=50 loops=1)
        Buffers: shared hit=51
Planning Time: 0.028 ms
Execution Time: 0.032 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..13.34 rows=50 width=147) (actual time=0.009..0.036 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_sched_jobs_name_id on scheduled_jobs  (cost=0.29..1316.41 rows=5039 width=147) (actual time=0.008..0.032 rows=50 loops=1)
        Index Cond: (ROW(name, id) < ROW('migrate-asset-alpha-sched'::text, '67b6a7d9-1d71-4797-90c5-a4c6edfd4ca4'::uuid))
        Buffers: shared hit=52
Planning Time: 0.063 ms
Execution Time: 0.043 ms
```

</details>

### scheduled_jobs · name · asc

Index: `idx_sched_jobs_name_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..8.15 rows=50 width=147) (actual time=0.006..0.027 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_sched_jobs_name_id on scheduled_jobs  (cost=0.29..1574.23 rows=10000 width=147) (actual time=0.005..0.024 rows=50 loops=1)
        Buffers: shared hit=52
Planning Time: 0.028 ms
Execution Time: 0.033 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..13.50 rows=50 width=147) (actual time=0.019..0.044 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_sched_jobs_name_id on scheduled_jobs  (cost=0.29..1311.01 rows=4959 width=147) (actual time=0.019..0.041 rows=50 loops=1)
        Index Cond: (ROW(name, id) > ROW('migrate-asset-final-sched'::text, '39ccbd88-e240-4dc2-b36c-1d130ad87929'::uuid))
        Buffers: shared hit=52
Planning Time: 0.068 ms
Execution Time: 0.053 ms
```

</details>

### scheduled_jobs · next_run_at · desc

Index: `idx_sched_jobs_next_run_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..7.63 rows=50 width=147) (actual time=0.005..0.028 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_sched_jobs_next_run_id on scheduled_jobs  (cost=0.29..1470.17 rows=10000 width=147) (actual time=0.005..0.026 rows=50 loops=1)
        Buffers: shared hit=52
Planning Time: 0.027 ms
Execution Time: 0.034 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..12.88 rows=50 width=147) (actual time=0.006..0.027 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_sched_jobs_next_run_id on scheduled_jobs  (cost=0.29..1259.64 rows=4999 width=147) (actual time=0.006..0.024 rows=50 loops=1)
        Index Cond: (ROW(next_run_at, id) < ROW('2026-06-12 01:12:42.97934+00'::timestamp with time zone, 'a2492803-0fdd-4e7b-b20c-70fc611ef25e'::uuid))
        Buffers: shared hit=52
Planning Time: 0.034 ms
Execution Time: 0.034 ms
```

</details>

### scheduled_jobs · next_run_at · asc

Index: `idx_sched_jobs_next_run_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..7.63 rows=50 width=147) (actual time=0.018..0.036 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_sched_jobs_next_run_id on scheduled_jobs  (cost=0.29..1470.17 rows=10000 width=147) (actual time=0.017..0.033 rows=50 loops=1)
        Buffers: shared hit=52
Planning Time: 0.026 ms
Execution Time: 0.042 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..12.88 rows=50 width=147) (actual time=0.007..0.031 rows=50 loops=1)
  Buffers: shared hit=51
  ->  Index Scan Backward using idx_sched_jobs_next_run_id on scheduled_jobs  (cost=0.29..1259.65 rows=5000 width=147) (actual time=0.006..0.027 rows=50 loops=1)
        Index Cond: (ROW(next_run_at, id) > ROW('2026-06-12 01:14:03.641387+00'::timestamp with time zone, '09ee04b6-2a81-41b8-a548-8de77d8e36bb'::uuid))
        Buffers: shared hit=51
Planning Time: 0.042 ms
Execution Time: 0.039 ms
```

</details>

### scheduled_jobs · updated_at · desc

Index: `idx_sched_jobs_updated_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..7.53 rows=50 width=147) (actual time=0.008..0.041 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_sched_jobs_updated_id on scheduled_jobs  (cost=0.29..1450.25 rows=10000 width=147) (actual time=0.008..0.037 rows=50 loops=1)
        Buffers: shared hit=52
Planning Time: 0.035 ms
Execution Time: 0.049 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..12.80 rows=50 width=147) (actual time=0.009..0.028 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_sched_jobs_updated_id on scheduled_jobs  (cost=0.29..1251.73 rows=4999 width=147) (actual time=0.008..0.025 rows=50 loops=1)
        Index Cond: (ROW(updated_at, id) < ROW('2026-05-13 00:19:55.620566+00'::timestamp with time zone, 'db974b5e-d7a9-46c8-8714-8f0a39686cd2'::uuid))
        Buffers: shared hit=52
Planning Time: 0.047 ms
Execution Time: 0.036 ms
```

</details>

### scheduled_jobs · updated_at · asc

Index: `idx_sched_jobs_updated_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..7.53 rows=50 width=147) (actual time=0.017..0.035 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_sched_jobs_updated_id on scheduled_jobs  (cost=0.29..1450.25 rows=10000 width=147) (actual time=0.016..0.032 rows=50 loops=1)
        Buffers: shared hit=52
Planning Time: 0.026 ms
Execution Time: 0.041 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..12.80 rows=50 width=147) (actual time=0.006..0.028 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_sched_jobs_updated_id on scheduled_jobs  (cost=0.29..1251.74 rows=5000 width=147) (actual time=0.006..0.025 rows=50 loops=1)
        Index Cond: (ROW(updated_at, id) > ROW('2026-05-13 00:21:10.362483+00'::timestamp with time zone, '8e4145af-d76c-4275-80ba-f2dc2c3c4f3e'::uuid))
        Buffers: shared hit=52
Planning Time: 0.035 ms
Execution Time: 0.035 ms
```

</details>

### reservations · created_at · desc

Index: `idx_reservations_created_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.29 rows=50 width=86) (actual time=0.005..0.035 rows=50 loops=1)
  Buffers: shared hit=51
  ->  Index Scan using idx_reservations_created_id on reservations  (cost=0.29..1202.16 rows=10000 width=86) (actual time=0.005..0.032 rows=50 loops=1)
        Buffers: shared hit=51
Planning:
  Buffers: shared hit=14
Planning Time: 0.069 ms
Execution Time: 0.041 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..10.28 rows=50 width=86) (actual time=0.007..0.031 rows=50 loops=1)
  Buffers: shared hit=51
  ->  Index Scan using idx_reservations_created_id on reservations  (cost=0.29..999.62 rows=4999 width=86) (actual time=0.006..0.028 rows=50 loops=1)
        Index Cond: (ROW(created_at, id) < ROW('2026-04-12 21:18:58.098465+00'::timestamp with time zone, '70eacb51-4063-45b1-a4a6-7f534ca409cb'::uuid))
        Buffers: shared hit=51
Planning Time: 0.048 ms
Execution Time: 0.038 ms
```

</details>

### reservations · created_at · asc

Index: `idx_reservations_created_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.29 rows=50 width=86) (actual time=0.006..0.038 rows=50 loops=1)
  Buffers: shared hit=51
  ->  Index Scan Backward using idx_reservations_created_id on reservations  (cost=0.29..1202.16 rows=10000 width=86) (actual time=0.006..0.036 rows=50 loops=1)
        Buffers: shared hit=51
Planning Time: 0.040 ms
Execution Time: 0.045 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..10.28 rows=50 width=86) (actual time=0.005..0.032 rows=50 loops=1)
  Buffers: shared hit=54
  ->  Index Scan Backward using idx_reservations_created_id on reservations  (cost=0.29..999.62 rows=4999 width=86) (actual time=0.005..0.029 rows=50 loops=1)
        Index Cond: (ROW(created_at, id) > ROW('2026-04-12 21:33:42.790331+00'::timestamp with time zone, '297f2f0c-499d-4b17-84e4-3e1764bb0d33'::uuid))
        Buffers: shared hit=54
Planning Time: 0.046 ms
Execution Time: 0.040 ms
```

</details>

### reservations · ends_at · desc

Index: `idx_reservations_ends_desc` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.25 rows=50 width=86) (actual time=0.006..0.027 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_reservations_ends_desc on reservations  (cost=0.29..1194.28 rows=10000 width=86) (actual time=0.005..0.024 rows=50 loops=1)
        Buffers: shared hit=52
Planning Time: 0.026 ms
Execution Time: 0.045 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..13.52 rows=50 width=86) (actual time=0.006..0.031 rows=50 loops=1)
  Buffers: shared hit=54
  ->  Index Scan Backward using idx_reservations_ends_asc on reservations  (cost=0.29..929.70 rows=3510 width=86) (actual time=0.006..0.028 rows=50 loops=1)
        Index Cond: (ROW(ends_at, id) < ROW('2026-06-27 04:05:26.918746+00'::timestamp with time zone, 'e066a98c-2215-4afd-bf23-c4e1adf6232d'::uuid))
        Buffers: shared hit=54
Planning Time: 0.046 ms
Execution Time: 0.038 ms
```

</details>

### reservations · ends_at · asc

Index: `idx_reservations_ends_asc` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.25 rows=50 width=86) (actual time=0.005..0.022 rows=50 loops=1)
  Buffers: shared hit=51
  ->  Index Scan Backward using idx_reservations_ends_desc on reservations  (cost=0.29..1194.28 rows=10000 width=86) (actual time=0.004..0.019 rows=50 loops=1)
        Buffers: shared hit=51
Planning Time: 0.025 ms
Execution Time: 0.040 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..13.52 rows=50 width=86) (actual time=0.006..0.024 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_reservations_ends_asc on reservations  (cost=0.29..929.72 rows=3511 width=86) (actual time=0.006..0.022 rows=50 loops=1)
        Index Cond: (ROW(ends_at, id) > ROW('2026-06-27 04:08:34.43817+00'::timestamp with time zone, 'c2c075ca-c498-42ae-94ef-48c369b0bad0'::uuid))
        Buffers: shared hit=52
Planning Time: 0.034 ms
Execution Time: 0.031 ms
```

</details>

### reservations · name · desc

Index: `idx_reservations_name_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.35 rows=50 width=86) (actual time=0.005..0.035 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_reservations_name_id on reservations  (cost=0.29..1214.28 rows=10000 width=86) (actual time=0.005..0.020 rows=50 loops=1)
        Buffers: shared hit=52
Planning Time: 0.024 ms
Execution Time: 0.040 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..10.36 rows=50 width=86) (actual time=0.007..0.036 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_reservations_name_id on reservations  (cost=0.29..1003.41 rows=4979 width=86) (actual time=0.006..0.034 rows=50 loops=1)
        Index Cond: (ROW(name, id) < ROW('Janet-resv'::text, '46e891a9-02d8-4d46-8be2-da54c545ac04'::uuid))
        Buffers: shared hit=52
Planning Time: 0.059 ms
Execution Time: 0.043 ms
```

</details>

### reservations · name · asc

Index: `idx_reservations_name_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.35 rows=50 width=86) (actual time=0.006..0.026 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_reservations_name_id on reservations  (cost=0.29..1214.28 rows=10000 width=86) (actual time=0.006..0.023 rows=50 loops=1)
        Buffers: shared hit=52
Planning Time: 0.029 ms
Execution Time: 0.033 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..10.36 rows=50 width=86) (actual time=0.010..0.030 rows=50 loops=1)
  Buffers: shared hit=51
  ->  Index Scan Backward using idx_reservations_name_id on reservations  (cost=0.29..1003.39 rows=4978 width=86) (actual time=0.010..0.028 rows=50 loops=1)
        Index Cond: (ROW(name, id) > ROW('Janet-resv'::text, '48a31884-ff7a-4214-b508-8b09198339b3'::uuid))
        Buffers: shared hit=51
Planning Time: 0.049 ms
Execution Time: 0.038 ms
```

</details>

### reservations · starts_at · desc

Index: `idx_reservations_starts_desc` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.29 rows=50 width=86) (actual time=0.005..0.024 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_reservations_starts_asc on reservations  (cost=0.29..1202.27 rows=10000 width=86) (actual time=0.005..0.022 rows=50 loops=1)
        Buffers: shared hit=52
Planning Time: 0.027 ms
Execution Time: 0.030 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..13.60 rows=50 width=86) (actual time=0.006..0.024 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan Backward using idx_reservations_starts_asc on reservations  (cost=0.29..929.34 rows=3490 width=86) (actual time=0.006..0.022 rows=50 loops=1)
        Index Cond: (ROW(starts_at, id) < ROW('2026-04-27 11:47:41.651165+00'::timestamp with time zone, '61d14fca-6837-4a06-b5cd-c56416b9c48c'::uuid))
        Buffers: shared hit=52
Planning Time: 0.040 ms
Execution Time: 0.033 ms
```

</details>

### reservations · starts_at · asc

Index: `idx_reservations_starts_asc` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..6.29 rows=50 width=86) (actual time=0.004..0.025 rows=50 loops=1)
  Buffers: shared hit=52
  ->  Index Scan using idx_reservations_starts_asc on reservations  (cost=0.29..1202.27 rows=10000 width=86) (actual time=0.004..0.022 rows=50 loops=1)
        Buffers: shared hit=52
Planning Time: 0.027 ms
Execution Time: 0.031 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..13.59 rows=50 width=86) (actual time=0.004..0.024 rows=50 loops=1)
  Buffers: shared hit=53
  ->  Index Scan using idx_reservations_starts_asc on reservations  (cost=0.29..929.35 rows=3491 width=86) (actual time=0.004..0.022 rows=50 loops=1)
        Index Cond: (ROW(starts_at, id) > ROW('2026-04-27 11:47:41.651165+00'::timestamp with time zone, '61d14fca-6837-4a06-b5cd-c56416b9c48c'::uuid))
        Buffers: shared hit=53
Planning Time: 0.035 ms
Execution Time: 0.031 ms
```

</details>

### agent_enrollments · created_at · desc

Index: `idx_agent_enr_created_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.28..14.64 rows=50 width=169) (actual time=0.010..0.060 rows=50 loops=1)
  Buffers: shared hit=96
  ->  Index Scan using idx_agent_enr_created_id on agent_enrollments  (cost=0.28..1153.22 rows=4015 width=169) (actual time=0.010..0.057 rows=50 loops=1)
        Filter: (expires_at > now())
        Rows Removed by Filter: 46
        Buffers: shared hit=96
Planning:
  Buffers: shared hit=17
Planning Time: 0.096 ms
Execution Time: 0.066 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.28..24.41 rows=50 width=169) (actual time=0.008..0.055 rows=50 loops=1)
  Buffers: shared hit=109
  ->  Index Scan using idx_agent_enr_created_id on agent_enrollments  (cost=0.28..975.41 rows=2021 width=169) (actual time=0.007..0.052 rows=50 loops=1)
        Index Cond: (ROW(created_at, id) < ROW('2026-05-24 10:40:55.889416+00'::timestamp with time zone, '54c3088c-dae0-4a2e-981d-eef0583fe62f'::uuid))
        Filter: (expires_at > now())
        Rows Removed by Filter: 57
        Buffers: shared hit=109
Planning Time: 0.038 ms
Execution Time: 0.062 ms
```

</details>

### agent_enrollments · created_at · asc

Index: `idx_agent_enr_created_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.28..14.64 rows=50 width=169) (actual time=0.005..0.054 rows=50 loops=1)
  Buffers: shared hit=122
  ->  Index Scan Backward using idx_agent_enr_created_id on agent_enrollments  (cost=0.28..1153.22 rows=4015 width=169) (actual time=0.005..0.051 rows=50 loops=1)
        Filter: (expires_at > now())
        Rows Removed by Filter: 68
        Buffers: shared hit=122
Planning Time: 0.218 ms
Execution Time: 0.060 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.28..24.60 rows=50 width=169) (actual time=0.006..0.045 rows=50 loops=1)
  Buffers: shared hit=97
  ->  Index Scan Backward using idx_agent_enr_created_id on agent_enrollments  (cost=0.28..970.19 rows=1994 width=169) (actual time=0.005..0.042 rows=50 loops=1)
        Index Cond: (ROW(created_at, id) > ROW('2026-05-24 10:40:55.889416+00'::timestamp with time zone, '54c3088c-dae0-4a2e-981d-eef0583fe62f'::uuid))
        Filter: (expires_at > now())
        Rows Removed by Filter: 43
        Buffers: shared hit=97
Planning Time: 0.052 ms
Execution Time: 0.052 ms
```

</details>

### agent_enrollments · expires_at · desc

Index: `idx_agent_enr_expires_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..12.73 rows=50 width=169) (actual time=0.008..0.035 rows=50 loops=1)
  Buffers: shared hit=66
  ->  Index Scan using idx_agent_enr_expires_id on agent_enrollments  (cost=0.29..999.47 rows=4015 width=169) (actual time=0.008..0.032 rows=50 loops=1)
        Index Cond: (expires_at > now())
        Filter: (consumed_at IS NULL)
        Rows Removed by Filter: 15
        Buffers: shared hit=66
Planning Time: 0.045 ms
Execution Time: 0.041 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..16.06 rows=50 width=169) (actual time=0.012..0.055 rows=50 loops=1)
  Buffers: shared hit=63
  ->  Index Scan using idx_agent_enr_expires_id on agent_enrollments  (cost=0.29..951.06 rows=3014 width=169) (actual time=0.011..0.052 rows=50 loops=1)
        Index Cond: ((expires_at > now()) AND (ROW(expires_at, id) < ROW('2026-05-31 10:55:52.902512+00'::timestamp with time zone, '2ed2e9c1-14fd-4080-b8ff-906d1261d20b'::uuid)))
        Filter: (consumed_at IS NULL)
        Rows Removed by Filter: 11
        Buffers: shared hit=63
Planning Time: 0.062 ms
Execution Time: 0.066 ms
```

</details>

### agent_enrollments · expires_at · asc

Index: `idx_agent_enr_expires_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.29..12.73 rows=50 width=169) (actual time=0.007..0.033 rows=50 loops=1)
  Buffers: shared hit=60
  ->  Index Scan Backward using idx_agent_enr_expires_id on agent_enrollments  (cost=0.29..999.47 rows=4015 width=169) (actual time=0.007..0.031 rows=50 loops=1)
        Index Cond: (expires_at > now())
        Filter: (consumed_at IS NULL)
        Rows Removed by Filter: 8
        Buffers: shared hit=60
Planning Time: 0.039 ms
Execution Time: 0.041 ms
```

</details>

<details><summary>Cursor-resume plan</summary>

```
Limit  (cost=0.29..41.68 rows=50 width=169) (actual time=0.008..0.043 rows=50 loops=1)
  Buffers: shared hit=72
  ->  Index Scan Backward using idx_agent_enr_expires_id on agent_enrollments  (cost=0.29..828.99 rows=1001 width=169) (actual time=0.008..0.041 rows=50 loops=1)
        Index Cond: ((expires_at > now()) AND (ROW(expires_at, id) > ROW('2026-05-31 10:55:52.902512+00'::timestamp with time zone, '2ed2e9c1-14fd-4080-b8ff-906d1261d20b'::uuid)))
        Filter: (consumed_at IS NULL)
        Rows Removed by Filter: 19
        Buffers: shared hit=72
Planning Time: 0.047 ms
Execution Time: 0.053 ms
```

</details>

