package api

import (
	"errors"
	"net/url"
	"strconv"
)

// taskLogOrder selects WHICH rows a log page contains, not the order of the
// items inside it: items are ascending by seq in both directions.
type taskLogOrder string

const (
	taskLogOrderAsc  taskLogOrder = "asc"
	taskLogOrderDesc taskLogOrder = "desc"
)

// taskLogQuery is the validated query string of GET /v1/tasks/{id}/logs.
// Exactly one cursor is ever populated: SinceSeq ascending, BeforeSeq
// descending.
type taskLogQuery struct {
	Limit     int32
	Order     taskLogOrder
	SinceSeq  int64
	BeforeSeq int64
}

// parseTaskLogQuery validates the query string and returns a 400-worthy error
// whose message is written to the client verbatim. The handler must keep
// calling it AFTER its existence check, or the endpoint's 404-before-400
// precedence inverts; TestTaskLogs_UnknownTaskIs404AheadOfParameterValidation
// goes RED when it does.
//
// A cursor for the wrong direction is an error, never an ignored parameter:
// ignoring it leaves a client looping over one page while believing it is
// paging.
func parseTaskLogQuery(v url.Values) (taskLogQuery, error) {
	q := taskLogQuery{Limit: 50, Order: taskLogOrderAsc}

	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 200 {
			return taskLogQuery{}, errors.New("limit must be 1..200")
		}
		q.Limit = int32(n)
	}

	// Allow-list. A deny-list would fail open on the next value added.
	switch s := v.Get("order"); s {
	case "", string(taskLogOrderAsc):
		q.Order = taskLogOrderAsc
	case string(taskLogOrderDesc):
		q.Order = taskLogOrderDesc
	default:
		return taskLogQuery{}, errors.New("order must be asc or desc")
	}

	if s := v.Get("since_seq"); s != "" {
		if q.Order == taskLogOrderDesc {
			return taskLogQuery{}, errors.New("since_seq is not valid with order=desc; use before_seq")
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			return taskLogQuery{}, errors.New("since_seq must be a non-negative integer")
		}
		q.SinceSeq = n
	}

	if s := v.Get("before_seq"); s != "" {
		if q.Order != taskLogOrderDesc {
			return taskLogQuery{}, errors.New("before_seq requires order=desc")
		}
		n, err := strconv.ParseInt(s, 10, 64)
		// 0 is rejected rather than served as an empty page: the contract says
		// to stop when prev_seq is 0 rather than to send it back.
		if err != nil || n < 1 {
			return taskLogQuery{}, errors.New("before_seq must be a positive integer")
		}
		q.BeforeSeq = n
	}

	return q, nil
}
