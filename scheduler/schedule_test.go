package scheduler

import (
	"testing"
	"time"
)

func instant(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestDailyNext(t *testing.T) {
	for _, tt := range []struct {
		name, zone, after, want string
		hhmm                    int
	}{
		{"ordinary", "UTC", "2026-09-07T07:00:00Z", "2026-09-07T08:30:00Z", 830},
		{"exact time moves forward", "UTC", "2026-09-07T08:30:00Z", "2026-09-08T08:30:00Z", 830},
		{"quarter-hour offset", "Asia/Kathmandu", "2026-09-07T00:00:00Z", "2026-09-07T02:15:00Z", 800},
		{"spring gap skipped", "America/New_York", "2026-03-08T05:00:00Z", "2026-03-09T06:30:00Z", 230},
		{"fall first occurrence", "America/New_York", "2026-11-01T04:00:00Z", "2026-11-01T05:30:00Z", 130},
		{"fall second occurrence suppressed", "America/New_York", "2026-11-01T05:31:00Z", "2026-11-02T06:30:00Z", 130},
		{"half-hour DST gap", "Australia/Lord_Howe", "2026-10-03T13:00:00Z", "2026-10-04T15:15:00Z", 215},
		{"date line", "Pacific/Kiritimati", "2026-12-31T23:00:00Z", "2027-01-01T18:00:00Z", 800},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (Daily{LocalTime: tt.hhmm, Timezone: tt.zone}).Next(instant(tt.after))
			if err != nil || !got.Equal(instant(tt.want)) {
				t.Fatalf("got %s, %v; want %s", got, err, tt.want)
			}
		})
	}
}

func TestInvalidDaily(t *testing.T) {
	for _, d := range []Daily{{-1, "UTC"}, {1260, "UTC"}, {2400, "UTC"}, {800, ""}, {800, "Local"}, {800, "Mars/Olympus"}} {
		if _, err := d.Next(time.Now()); err == nil {
			t.Fatalf("accepted %+v", d)
		}
	}
}

func TestKeyDoesNotConfuseDelimiters(t *testing.T) {
	if Key("a", "b/c") == Key("a/b", "c") {
		t.Fatal("identity collision")
	}
}
