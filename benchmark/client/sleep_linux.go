package main

import (
	"syscall"
	"time"
)

const prSetTimerSlack = 29

// The kernel adds a default 50 µs of timer slack to every sleep on this thread, which
// would still show up as scheduling error at tens of thousands of req/s.
func tightenTimerSlack() {
	syscall.RawSyscall(syscall.SYS_PRCTL, prSetTimerSlack, 1, 0)
}

func preciseSleep(d time.Duration) {
	ts := syscall.NsecToTimespec(d.Nanoseconds())
	for {
		var remaining syscall.Timespec
		if err := syscall.Nanosleep(&ts, &remaining); err != syscall.EINTR {
			return
		}
		ts = remaining
	}
}
