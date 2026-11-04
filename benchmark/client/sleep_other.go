//go:build !linux

package main

import "time"

func tightenTimerSlack() {}

func preciseSleep(d time.Duration) {
	time.Sleep(d)
}
