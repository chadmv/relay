package worker

import (
	"testing"
	"time"
)

// SetSendTimeoutForTest lowers the Send buffer-full timeout for the duration of
// t. Restores the previous value on cleanup.
func SetSendTimeoutForTest(t *testing.T, d time.Duration) {
	t.Helper()
	prev := sendTimeout
	sendTimeout = d
	t.Cleanup(func() { sendTimeout = prev })
}
