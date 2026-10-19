package outbox

import (
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	cases := map[int]time.Duration{
		0: 5 * time.Second, 1: 5 * time.Second, 2: 10 * time.Second, 3: 20 * time.Second,
		10: 2560 * time.Second, 11: time.Hour, 50: time.Hour,
	}
	for attempts, want := range cases {
		if got := Backoff(attempts); got != want {
			t.Errorf("Backoff(%d) = %v, want %v", attempts, got, want)
		}
	}
}

func TestValidTopic(t *testing.T) {
	for topic, want := range map[string]bool{
		"stock.changed": true, "obligation.due_changed": true, "a.b.c": true,
		"stock": false, "Stock.changed": false, "stock.": false, ".x": false, "stock changed": false,
	} {
		if got := ValidTopic(topic); got != want {
			t.Errorf("ValidTopic(%q) = %v", topic, got)
		}
	}
}

func TestSubscribeRejectsInvalidTopic(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("invalid topic accepted")
		}
	}()
	NewDispatcher(nil, nil, nil, nil).Subscribe("bad", nil)
}

func TestNextPause(t *testing.T) {
	const interval = 2 * time.Second
	cases := []struct {
		prev time.Duration
		busy bool
		want time.Duration
	}{
		{interval, true, interval},
		{8 * time.Second, true, interval},
		{interval, false, 4 * time.Second},
		{8 * time.Second, false, maxIdleInterval},
		{maxIdleInterval, false, maxIdleInterval},
		{0, false, interval},
	}
	for _, tc := range cases {
		if got := nextPause(tc.prev, interval, tc.busy); got != tc.want {
			t.Errorf("nextPause(%v, busy=%v) = %v, want %v", tc.prev, tc.busy, got, tc.want)
		}
	}
	// An interval above the idle cap is never shortened.
	if got := nextPause(time.Minute, time.Minute, false); got != time.Minute {
		t.Errorf("long interval idle pause = %v", got)
	}
}
