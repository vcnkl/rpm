package watcher

import (
	"sync"
	"time"
)

type Debouncer struct {
	duration time.Duration
	timer    *time.Timer
	mu       sync.Mutex
}

func NewDebouncer(duration time.Duration) *Debouncer {
	return &Debouncer{
		duration: duration,
	}
}

func (d *Debouncer) Trigger(fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
	}

	d.timer = time.AfterFunc(d.duration, fn)
}
