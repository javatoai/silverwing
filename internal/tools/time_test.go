package tools

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestConvertTimeSourceSupportedInputs(t *testing.T) {
	for _, input := range []string{
		"2024-02-29T12:34", "2024-02-29T12:34:00",
		"2024-02-29T12:34+08:00", "2024-02-29T12:34:00+08:00[Asia/Shanghai]",
	} {
		t.Run(input, func(t *testing.T) {
			result, err := ConvertTime(input, "AUTO", "Asia/Shanghai")
			if err != nil || result.Formatted != "2024-02-29 12:34:00.000" {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
	for _, value := range []int64{-9223372036854775808, 9223372036854775807} {
		t.Run(strconv.FormatInt(value, 10), func(t *testing.T) {
			result, err := ConvertTime(strconv.FormatInt(value, 10), "MILLISECONDS", "UTC")
			if err != nil || result.EpochMilliseconds != value {
				t.Fatalf("epoch=%d error=%v", result.EpochMilliseconds, err)
			}
		})
	}
}

func TestTimeDisplayFormatRoundTripsMilliseconds(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	for _, millisecond := range []int{0, 1, 123, 999} {
		instant := time.Date(2026, 10, 15, 14, 29, 33, millisecond*int(time.Millisecond), location)
		shown, err := timeResult(instant, location, "DATE_TIME")
		if err != nil {
			t.Fatal(err)
		}
		converted, err := ConvertTime(" \t"+shown.Formatted+"\r\n", "AUTO", "")
		if err != nil || converted.EpochMilliseconds != instant.UnixMilli() || converted.Formatted != shown.Formatted || strings.ContainsAny(shown.Formatted, "年月日") {
			t.Fatalf("display/copy round trip lost precision: shown=%+v converted=%+v err=%v", shown, converted, err)
		}
	}
}

func TestTimeCommonAndLegacyChineseDateInputs(t *testing.T) {
	for _, input := range []string{"2026-10-15 14:29:33", "2026年10月15日 14:29:33"} {
		for _, fraction := range []struct {
			input        string
			milliseconds int64
		}{{"", 0}, {".001", 1}, {".123", 123}, {".1", 100}, {".123456789", 123}} {
			converted, err := ConvertTime(input+fraction.input, "AUTO", "Asia/Shanghai")
			if err != nil || converted.EpochMilliseconds != 1792045773000+fraction.milliseconds {
				t.Fatalf("supported numeric/legacy input failed: %q result=%+v err=%v", input+fraction.input, converted, err)
			}
		}
	}
	for _, test := range []struct{ input, kind, formatted string }{
		{"1792045773", "SECONDS_TIMESTAMP", "2026-10-15 14:29:33"},
		{"1792045773001", "MILLISECONDS_TIMESTAMP", "2026-10-15 14:29:33.001"},
	} {
		got, err := ConvertTime(test.input, "AUTO", "Asia/Shanghai")
		if err != nil || got.InputKind != test.kind || got.Formatted != test.formatted {
			t.Fatalf("timestamp behavior changed: %+v %v", got, err)
		}
	}
}

func TestTimeExplicitOffsetsAndFriendlyInvalidDates(t *testing.T) {
	for _, input := range []string{"2026-10-15T06:29:33.001Z", "2026-10-15T14:29:33.001+08:00[Asia/Shanghai]", "2026-10-15T12:29:33.001+06:00", "2026-10-15T14:29:33.001+08:00:00"} {
		got, err := ConvertTime(input, "AUTO", "Asia/Shanghai")
		if err != nil || got.EpochMilliseconds != 1792045773001 || got.Formatted != "2026-10-15 14:29:33.001" {
			t.Fatalf("explicit offset changed: %s %+v %v", input, got, err)
		}
	}
	for _, input := range []string{"2026-02-30 14:29:33.001", "2026年02月30日 14:29:33.001", "2026-13-15 14:29:33", "2026-10-15 24:29:33", "not-a-date"} {
		_, err := ConvertTime(input, "AUTO", "Asia/Shanghai")
		if err == nil || err.Error() != "无法识别日期时间，请输入有效日期或时间戳" {
			t.Fatalf("invalid input exposed a parser error: %q %v", input, err)
		}
	}
	for _, input := range []string{"2024-03-10 02:30:00", "2024年03月10日 02:30:00", "2024-11-03 01:30:00"} {
		_, err := ConvertTime(input, "AUTO", "America/New_York")
		if err == nil || !strings.Contains(err.Error(), "夏令时") {
			t.Fatalf("local-time gap/overlap handling changed: %q %v", input, err)
		}
	}
}

func TestConvertTimeRejectsInvalidZoneAndMillisecondOverflow(t *testing.T) {
	for _, input := range []string{
		"2024-02-29T12:34:00+08:00[Invalid/Zone]",
		"2024-02-29T12:34:00+08:00[]",
	} {
		if _, err := ConvertTime(input, "AUTO", "Asia/Shanghai"); err == nil {
			t.Errorf("accepted invalid zone: %s", input)
		}
	}
	if _, err := timeResult(time.Unix(9223372036854775, 808000000), time.UTC, "DATE_TIME"); err == nil {
		t.Error("accepted instant above the signed millisecond limit")
	}
}
