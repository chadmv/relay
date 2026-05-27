package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// caseResult is the outcome of running both EXPLAINs for one case.
type caseResult struct {
	Case        explainCase
	Status      string // "PASS" | "FAIL" | "ERROR"
	Reason      string // populated when Status != PASS
	InitialPlan string
	CursorPlan  string
}

// runExplain captures the full text of an EXPLAIN result.
func runExplain(ctx context.Context, pool *pgxpool.Pool, sql string) (string, error) {
	rows, err := pool.Query(ctx, sql)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", err
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return sb.String(), rows.Err()
}

// indexScanRE matches the first node line of an Index Scan plan. The
// leading whitespace and arrow are part of the EXPLAIN format; the
// capture group is the index name. Forward, backward, and index-only
// variants all parse.
var indexScanRE = regexp.MustCompile(
	`(?m)^\s*->\s*(?:Index Scan|Index Only Scan|Index Scan Backward) using (\S+)\b`)

// checkPlan inspects an EXPLAIN result and returns PASS / FAIL with a
// reason. The plan PASSes iff the first non-Limit node is an Index
// (Only|Backward) Scan whose index name is in the valid set.
//
// valid must contain at least one entry. When a nullable column has both
// an ASC and a DESC direction index, pass both; Postgres may scan either
// in either direction and the planner choice is nondeterministic.
func checkPlan(plan string, valid []string) (status, reason string) {
	if strings.Contains(plan, "Seq Scan") {
		return "FAIL", "plan contains Seq Scan"
	}
	if strings.Contains(plan, "->  Sort") {
		return "FAIL", "plan contains an explicit Sort node"
	}
	m := indexScanRE.FindStringSubmatch(plan)
	if m == nil {
		return "FAIL", "no Index Scan node found"
	}
	for _, v := range valid {
		if m[1] == v {
			return "PASS", ""
		}
	}
	if len(valid) == 1 {
		return "FAIL", fmt.Sprintf("used index %q, expected %q", m[1], valid[0])
	}
	return "FAIL", fmt.Sprintf("used index %q, expected one of %v", m[1], valid)
}

// explainCaseRun runs both EXPLAINs for a case and returns the result.
// Any SQL error produces Status="ERROR".
func explainCaseRun(ctx context.Context, pool *pgxpool.Pool, c explainCase) caseResult {
	r := caseResult{Case: c}
	initial, err := runExplain(ctx, pool, c.InitialSQL)
	if err != nil {
		r.Status = "ERROR"
		r.Reason = fmt.Sprintf("initial: %v", err)
		return r
	}
	r.InitialPlan = initial
	cursor, err := runExplain(ctx, pool, c.CursorSQL)
	if err != nil {
		r.Status = "ERROR"
		r.Reason = fmt.Sprintf("cursor: %v", err)
		return r
	}
	r.CursorPlan = cursor

	valid := c.ValidIndexes
	if len(valid) == 0 {
		valid = []string{c.ExpectedIndex}
	}
	if s, why := checkPlan(initial, valid); s != "PASS" {
		r.Status = "FAIL"
		r.Reason = "initial: " + why
		return r
	}
	if s, why := checkPlan(cursor, valid); s != "PASS" {
		r.Status = "FAIL"
		r.Reason = "cursor: " + why
		return r
	}
	r.Status = "PASS"
	return r
}
