package cron

import (
	"time"
)

type CronBinding struct {
	Schedule string
	Interval time.Duration
}

func (b CronBinding) String() string {
	if b.Schedule != "" {
		return "cron: " + b.Schedule
	}
	return "every " + b.Interval.String()
}

func Every(d time.Duration) CronBinding { return CronBinding{Interval: d} }
func Cron(expr string) CronBinding      { return CronBinding{Schedule: expr} }
