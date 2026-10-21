package domain

import (
	"time"
)

var shanghaiZone = func() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return location
}()

func FormatSilverWingTime(value time.Time) string {
	return value.In(shanghaiZone).Format("2006-01-02 15:04:05")
}

// ParseSilverWingTime reads persisted wall times in the same Shanghai zone as
// FormatSilverWingTime. Explicit RFC3339 offsets and fractional seconds remain
// authoritative, independently of the host's local time zone.
func ParseSilverWingTime(value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, nil
	}
	return time.ParseInLocation("2006-01-02 15:04:05", value, shanghaiZone)
}

func SilverWingLocalDate(value time.Time) time.Time {
	local := value.In(shanghaiZone)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, shanghaiZone)
}
