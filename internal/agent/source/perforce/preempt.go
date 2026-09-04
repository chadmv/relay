package perforce

import "strings"

// preemptReportedNoSuchFiles reports whether p4 told us the preempt's filespec
// matched nothing. p4 exits ZERO in that case and writes the message to stderr
// only, so this predicate is the whole of what stands between a typo'd
// exclusion and a full-size transfer of the subtree the operator asked to leave
// out. Two live routes reach it: a mistyped depot path, and an exclusion under
// a stream whose view renames a subtree, where toClientPath emits a client path
// that resolves to nothing.
//
// IT KEYS ON THE TEXT, NEVER ON AN EMPTY STREAM. Both of the successful
// readings look empty from one side: a real have-marking writes its per-file
// lines to stdout and leaves stderr EMPTY, and a warm workspace already at the
// target revision writes "file(s) up-to-date." to stderr with no stdout at all.
// testdata/p4-sync-k holds the captured artifacts all three readings are
// written against.
func preemptReportedNoSuchFiles(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), "no such file")
}
