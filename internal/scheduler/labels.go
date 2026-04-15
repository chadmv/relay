package scheduler

import (
	"encoding/json"
	"fmt"
)

// LabelMatch reports whether all key/value pairs in requires are present
// and matching in labels. Both arguments are JSONB-encoded map[string]string.
// An empty requires selector ({}) matches any labels.
func LabelMatch(requires, labels []byte) (bool, error) {
	var req map[string]string
	if err := json.Unmarshal(requires, &req); err != nil {
		return false, fmt.Errorf("parse requires: %w", err)
	}
	if len(req) == 0 {
		return true, nil
	}
	var lbl map[string]string
	if err := json.Unmarshal(labels, &lbl); err != nil {
		return false, fmt.Errorf("parse labels: %w", err)
	}
	for k, v := range req {
		if lbl[k] != v {
			return false, nil
		}
	}
	return true, nil
}
