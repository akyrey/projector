package runner

import "time"

// SetKillGrace shortens killGrace for a test and returns a restore func.
func SetKillGrace(d time.Duration) func() {
	prev := killGrace
	killGrace = d
	return func() { killGrace = prev }
}
