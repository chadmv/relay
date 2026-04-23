// Package schedrunner runs scheduled jobs: parses cron expressions, ticks on a
// timer, and fires eligible schedules by creating fresh job instances.
package schedrunner

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// parser accepts standard 5-field cron, predefined schedules (@hourly, etc.),
// and @every <duration>.
var parser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// Schedule wraps a parsed cron expression bound to a timezone.
type Schedule struct {
	inner cron.Schedule
	loc   *time.Location
}

// Next returns the next firing time strictly after the given base time.
func (s *Schedule) Next(base time.Time) time.Time {
	// robfig/cron evaluates in the location it was parsed with. Convert
	// base into that location, compute next, then return in UTC for storage.
	return s.inner.Next(base.In(s.loc)).UTC()
}

// ParseSchedule parses a cron expression against an IANA timezone name.
func ParseSchedule(expr, tz string) (*Schedule, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	inner, err := parser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	// Trick: SpecSchedule honors the time location of the argument passed to
	// Next. So we pass base.In(loc) in our Next() method above.
	return &Schedule{inner: inner, loc: loc}, nil
}

// ValidateMinInterval rejects schedules that would fire faster than min.
// Computes two consecutive fire times and checks the gap.
func ValidateMinInterval(expr, tz string, min time.Duration) error {
	s, err := ParseSchedule(expr, tz)
	if err != nil {
		return err
	}
	// Probe from a fixed anchor to keep results deterministic.
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := s.Next(anchor)
	b := s.Next(a)
	if b.Sub(a) < min {
		return fmt.Errorf("schedule fires faster than minimum interval %s (observed %s)", min, b.Sub(a))
	}
	return nil
}
