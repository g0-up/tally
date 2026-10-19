package clock

import (
	"testing"
	"time"
)

func TestBusinessDateUsesTenantZone(t *testing.T) {
	hcm, err := LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		at   time.Time
		want time.Time
	}{
		{"before local midnight", time.Date(2026, 1, 15, 16, 59, 59, 0, time.UTC), Date(2026, 1, 15)},
		{"after local midnight", time.Date(2026, 1, 15, 17, 0, 0, 0, time.UTC), Date(2026, 1, 16)},
		{"new year eve", time.Date(2026, 12, 31, 18, 0, 0, 0, time.UTC), Date(2027, 1, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BusinessDate(tc.at, hcm); !got.Equal(tc.want) {
				t.Fatalf("BusinessDate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFixedClock(t *testing.T) {
	start := time.Date(2026, 2, 1, 3, 0, 0, 123456789, time.FixedZone("x", 7*3600))
	c := NewFixed(start)
	if got := c.Now(); got.Location() != time.UTC || got.Nanosecond() != 123456000 {
		t.Fatalf("Now = %v, want UTC truncated to microseconds", got)
	}
	c.Advance(time.Hour)
	if got := c.Now().Sub(start.UTC().Truncate(time.Microsecond)); got != time.Hour {
		t.Fatalf("Advance moved %v, want 1h", got)
	}
	c.Set(start.Add(-time.Hour))
	if got := c.Now(); !got.Equal(start.Add(-time.Hour).Truncate(time.Microsecond)) {
		t.Fatalf("Set = %v", got)
	}
}

func TestSystemClockIsUTC(t *testing.T) {
	now := System{}.Now()
	if now.Location() != time.UTC || now.Nanosecond()%1000 != 0 {
		t.Fatalf("System.Now = %v, want UTC microseconds", now)
	}
}

func TestTruncateDate(t *testing.T) {
	got := TruncateDate(time.Date(2026, 3, 4, 23, 59, 0, 0, time.UTC))
	if !got.Equal(Date(2026, 3, 4)) {
		t.Fatalf("TruncateDate = %v", got)
	}
}

func TestLoadLocationRejectsEmptyAndUnknown(t *testing.T) {
	if _, err := LoadLocation(""); err == nil {
		t.Fatal("empty zone accepted")
	}
	if _, err := LoadLocation("Mars/Olympus"); err == nil {
		t.Fatal("unknown zone accepted")
	}
}
