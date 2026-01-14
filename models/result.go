package models

import "time"

type Result struct {
	Executed []string
	Skipped  []string
	Failed   []FailedTarget
	Duration time.Duration
}

type FailedTarget struct {
	ID       string
	Error    error
	ExitCode int
}
