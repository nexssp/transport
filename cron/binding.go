package cron

import (
	"time"
)

// Binding defines a cron schedule binding: either a cron expression or a fixed interval.
type Binding struct {
	Schedule string
	Interval time.Duration
}

func (b Binding) String() string {
	if b.Schedule != "" {
		return "cron: " + b.Schedule
	}
	return "every " + b.Interval.String()
}

func Every(d time.Duration) Binding { return Binding{Interval: d} }
func Cron(expr string) Binding      { return Binding{Schedule: expr} }
