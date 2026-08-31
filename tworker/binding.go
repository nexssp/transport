package tworker

import (
	"time"
)

// Binding defines a background worker schedule.
type Binding struct {
	Interval       time.Duration
	RestartOnError bool
}

func (b Binding) String() string {
	return "worker: every " + b.Interval.String()
}

// Every binds an action to run as a background worker continuously.
func Every(d time.Duration) Binding {
	return Binding{Interval: d, RestartOnError: true}
}
