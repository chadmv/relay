package schedrunner

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseSchedule_StandardCron(t *testing.T) {
	s, err := ParseSchedule("0 2 * * *", "UTC")
	require.NoError(t, err)
	base := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)
	next := s.Next(base)
	require.Equal(t, time.Date(2026, 4, 22, 2, 0, 0, 0, time.UTC), next)
}

func TestParseSchedule_Predefined(t *testing.T) {
	s, err := ParseSchedule("@hourly", "UTC")
	require.NoError(t, err)
	base := time.Date(2026, 4, 22, 3, 17, 0, 0, time.UTC)
	next := s.Next(base)
	require.Equal(t, time.Date(2026, 4, 22, 4, 0, 0, 0, time.UTC), next)
}

func TestParseSchedule_EveryDuration(t *testing.T) {
	s, err := ParseSchedule("@every 15m", "UTC")
	require.NoError(t, err)
	base := time.Date(2026, 4, 22, 3, 0, 0, 0, time.UTC)
	next := s.Next(base)
	require.Equal(t, time.Date(2026, 4, 22, 3, 15, 0, 0, time.UTC), next)
}

func TestParseSchedule_Timezone(t *testing.T) {
	// "0 9 * * *" in America/Los_Angeles = 16:00 UTC during PDT (April, UTC-7).
	s, err := ParseSchedule("0 9 * * *", "America/Los_Angeles")
	require.NoError(t, err)
	base := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)
	next := s.Next(base)
	require.Equal(t, 16, next.UTC().Hour())
	require.Equal(t, 0, next.UTC().Minute())
}

func TestParseSchedule_InvalidCron(t *testing.T) {
	_, err := ParseSchedule("not a cron", "UTC")
	require.Error(t, err)
}

func TestParseSchedule_InvalidTimezone(t *testing.T) {
	_, err := ParseSchedule("@hourly", "Not/A_Real_Zone")
	require.Error(t, err)
}

func TestValidateMinInterval_TooShort(t *testing.T) {
	err := ValidateMinInterval("@every 5s", "UTC", 30*time.Second)
	require.Error(t, err)
}

func TestValidateMinInterval_LongEnough(t *testing.T) {
	err := ValidateMinInterval("@every 30s", "UTC", 30*time.Second)
	require.NoError(t, err)
}

func TestValidateMinInterval_StandardCron(t *testing.T) {
	// "* * * * *" fires every minute (60s), well above a 30s minimum.
	err := ValidateMinInterval("* * * * *", "UTC", 30*time.Second)
	require.NoError(t, err)
}
