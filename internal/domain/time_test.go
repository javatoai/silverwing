package domain

import (
	"testing"
	"time"
)

func TestParseSilverWingTime(t *testing.T) {
	originalLocal := time.Local
	defer func() { time.Local = originalLocal }()
	instant := time.Date(2026, 10, 15, 10, 0, 12, 345678901, time.UTC)
	for _, location := range []*time.Location{time.UTC, time.FixedZone("UTC-08", -8*60*60), time.FixedZone("UTC+12", 12*60*60)} {
		t.Run(location.String(), func(t *testing.T) {
			time.Local = location // Sequential only; never t.Parallel with Local.
			formatted := FormatSilverWingTime(instant)
			if formatted != "2026-10-15 18:00:12" {
				t.Fatalf("format=%q", formatted)
			}
			parsed, err := ParseSilverWingTime(formatted)
			if err != nil || !parsed.Equal(instant.Truncate(time.Second)) {
				t.Fatalf("round trip=%v err=%v", parsed, err)
			}
			_, offset := parsed.Zone()
			if offset != 8*60*60 {
				t.Fatalf("persisted zone offset=%d", offset)
			}
			fractional, err := ParseSilverWingTime("2026-10-15 18:00:12.345678901")
			if err != nil || !fractional.Equal(instant) {
				t.Fatalf("persisted fractional time=%v err=%v", fractional, err)
			}
			for _, text := range []string{"2026-10-15T10:00:12.345678901Z", "2026-10-15T02:00:12.345678901-08:00", "2026-10-15T22:00:12.345678901+12:00"} {
				parsed, err := ParseSilverWingTime(text)
				if err != nil || !parsed.Equal(instant) || parsed.Format(time.RFC3339Nano) != text {
					t.Fatalf("explicit %q parsed=%v err=%v", text, parsed, err)
				}
			}
		})
	}
	for _, text := range []string{"", "invalid", "2026-02-30 12:00:00", "2026/10/15 18:00:12"} {
		if _, err := ParseSilverWingTime(text); err == nil {
			t.Fatalf("unsupported input accepted: %q", text)
		}
	}
}
