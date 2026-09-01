package scheduler

// jobURL and taskURL render the browser-facing links the coordinator puts on
// DispatchTask.
//
// AN EMPTY ARGUMENT YIELDS "". That is the single gate for "this field goes on
// the wire empty", so the emptiness decision lives here instead of at the call
// site, and a consumer of the resulting environment variable needs one check
// rather than a second one for "set but blank".
//
// Plain concatenation with no separator logic: NewDispatcher trims the trailing
// slash off base.
//
// THE IDS ARE NOT ESCAPED, on a stated premise: both are uuidStr output over
// pgtype.UUID values read off the claimed row, so they can contain only
// [0-9a-f-]. If task or job ids ever stop being UUIDs, the escaping question
// reopens here.
func jobURL(base, jobID string) string {
	if base == "" || jobID == "" {
		return ""
	}
	return base + "/jobs/" + jobID
}

func taskURL(base, jobID, taskID string) string {
	if base == "" || jobID == "" || taskID == "" {
		return ""
	}
	return base + "/jobs/" + jobID + "/tasks/" + taskID
}
