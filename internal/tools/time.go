package tools

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

type TimeConversion struct {
	InputKind         string `json:"inputKind"`
	EpochSeconds      int64  `json:"epochSeconds"`
	EpochMilliseconds int64  `json:"epochMilliseconds"`
	Formatted         string `json:"formatted"`
	ISO8601           string `json:"iso8601"`
}

var integer = regexp.MustCompile(`^[+-]?\d+$`)
var dateTimeOffset = regexp.MustCompile(`[+-]([0-9]{2})(?::?([0-9]{2}))?(?::?([0-9]{2}))?$`)

// TimeInputBlank shares the parser's Kotlin/JVM whitespace rules with the UI.
func TimeInputBlank(input string) bool {
	return strings.TrimFunc(input, sourceWhitespace) == ""
}

func ConvertTime(input, unit, zone string) (TimeConversion, error) {
	if err := checkSize(input); err != nil {
		return TimeConversion{}, err
	}
	input = strings.TrimFunc(input, sourceWhitespace)
	if input == "" {
		return TimeConversion{}, errors.New("请输入时间戳或日期时间")
	}
	if zone == "" {
		zone = "Asia/Shanghai"
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return TimeConversion{}, err
	}
	kind := "DATE_TIME"
	var instant time.Time
	if integer.MatchString(input) {
		if unit == "" || unit == "AUTO" {
			switch len(strings.TrimLeft(input, "+-")) {
			case 10:
				unit = "SECONDS"
			case 13:
				unit = "MILLISECONDS"
			default:
				return TimeConversion{}, errors.New("无法自动识别：请输入 10 位秒时间戳或 13 位毫秒时间戳")
			}
		}
		value, err := strconv.ParseInt(input, 10, 64)
		if err != nil {
			return TimeConversion{}, errors.New("时间戳超出整数范围")
		}
		switch strings.ToUpper(unit) {
		case "SECONDS":
			instant = time.Unix(value, 0)
			kind = "SECONDS_TIMESTAMP"
		case "MILLISECONDS":
			instant = time.UnixMilli(value)
			kind = "MILLISECONDS_TIMESTAMP"
		default:
			return TimeConversion{}, errors.New("无效时间戳单位")
		}
	} else {
		value := input
		if at := strings.IndexByte(value, '['); at >= 0 && strings.HasSuffix(value, "]") {
			if err := validateDateZone(value[at+1 : len(value)-1]); err != nil {
				return TimeConversion{}, err
			}
			value = value[:at]
		}
		value, err = sourceDateTimeInput(value)
		if err != nil {
			return TimeConversion{}, err
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00", "2006-01-02T15:04:05Z07:00:00", "2006-01-02T15:04Z07:00:00", "2006-01-02T15:04:05Z07", "2006-01-02T15:04Z07", "2006-01-02T15:04:05Z0700"} {
			instant, err = time.Parse(layout, value)
			if err == nil {
				break
			}
		}
		if err != nil {
			for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04", "2006-01-02 15:04:05.999999999", "2006年01月02日 15:04:05.999999999", "2006-01-02"} {
				instant, err = time.ParseInLocation(layout, value, location)
				if err == nil {
					// Strictly reject nonexistent and ambiguous local wall-clock times.
					wallInput, parseErr := time.Parse(layout, value)
					if parseErr != nil || instant.In(location).Format(layout) != wallInput.Format(layout) {
						err = errors.New("该本地时间处于夏令时跳变区间，请填写带偏移量的时间")
						break
					}
					wall := instant.In(location).Format("2006-01-02 15:04:05.999999999")
					_, offset := instant.Zone()
					start, end := instant.ZoneBounds()
					var adjacent []time.Time
					if !start.IsZero() {
						adjacent = append(adjacent, start.Add(-time.Nanosecond))
					}
					if !end.IsZero() {
						adjacent = append(adjacent, end)
					}
					for _, boundary := range adjacent {
						_, otherOffset := boundary.In(location).Zone()
						candidate := instant.Add(time.Duration(offset-otherOffset) * time.Second)
						if otherOffset != offset && candidate.In(location).Format("2006-01-02 15:04:05.999999999") == wall {
							err = errors.New("该本地时间存在夏令时歧义，请填写带偏移量的时间")
							break
						}
					}
					break
				}
			}
		}
		if err != nil {
			var parseErr *time.ParseError
			if errors.As(err, &parseErr) {
				return TimeConversion{}, errors.New("无法识别日期时间，请输入有效日期或时间戳")
			}
			return TimeConversion{}, err
		}
	}
	return timeResult(instant, location, kind)
}

// Keep the standard parser, guarding its permissive fractional/offset rules
// where Source's strict formatters reject input. ISO T/Z are case insensitive.
func sourceDateTimeInput(value string) (string, error) {
	invalid := func() (string, error) {
		return "", errors.New("无法识别日期时间；请使用时间戳、ISO 8601 或 yyyy-MM-dd HH:mm:ss 格式")
	}
	for i := 0; i < len(value); i++ {
		if value[i] == ',' {
			return invalid()
		}
		if value[i] == '.' {
			end := i + 1
			for end < len(value) && value[end] >= '0' && value[end] <= '9' {
				end++
			}
			if end-i-1 > 9 {
				return invalid()
			}
		}
	}
	separator := strings.IndexAny(value, "Tt")
	if separator >= 0 {
		if value[separator] == 't' {
			value = value[:separator] + "T" + value[separator+1:]
		}
		if strings.HasSuffix(value, "z") {
			value = value[:len(value)-1] + "Z"
		}
		if offset := dateTimeOffset.FindStringSubmatch(value[separator+1:]); offset != nil {
			hours, _ := strconv.Atoi(offset[1])
			minutes, _ := strconv.Atoi(offset[2])
			seconds, _ := strconv.Atoi(offset[3])
			if hours > 18 || minutes > 59 || seconds > 59 || hours == 18 && (minutes != 0 || seconds != 0) {
				return invalid()
			}
		}
	}
	return value, nil
}

// A bracketed region must be a valid ZoneId; it cannot be silently discarded.
// Offset date-times still determine the instant from their explicit offset.
func validateDateZone(zone string) error {
	if zone != "" {
		if _, err := time.LoadLocation(zone); err == nil {
			return nil
		}
		for _, prefix := range []string{"UTC", "GMT", "UT", ""} {
			if !strings.HasPrefix(zone, prefix) {
				continue
			}
			offset := strings.TrimPrefix(zone, prefix)
			if !strings.HasPrefix(offset, "+") && !strings.HasPrefix(offset, "-") {
				continue
			}
			for _, layout := range []string{"Z07:00:00", "Z07:00", "Z0700", "Z07"} {
				parsed, err := time.Parse(layout, offset)
				if err == nil {
					_, seconds := parsed.Zone()
					if seconds >= -18*60*60 && seconds <= 18*60*60 {
						return nil
					}
				}
			}
		}
	}
	return errors.New("无法识别日期时间的时区：" + zone)
}
func CurrentTime(zone string) (TimeConversion, error) {
	if zone == "" {
		zone = "Asia/Shanghai"
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return TimeConversion{}, err
	}
	return timeResult(time.Now(), location, "DATE_TIME")
}
func timeResult(instant time.Time, location *time.Location, kind string) (TimeConversion, error) {
	seconds := instant.Unix()
	milliseconds := int64(instant.Nanosecond() / int(time.Millisecond))
	if seconds > math.MaxInt64/1000 ||
		seconds == math.MaxInt64/1000 && milliseconds > math.MaxInt64%1000 ||
		seconds < math.MinInt64/1000-1 ||
		seconds == math.MinInt64/1000-1 && milliseconds < 1000+math.MinInt64%1000 {
		return TimeConversion{}, errors.New("日期超出毫秒时间戳范围")
	}
	format := "2006-01-02 15:04:05.000"
	if kind == "SECONDS_TIMESTAMP" {
		format = "2006-01-02 15:04:05"
	}
	local := instant.In(location)
	return TimeConversion{InputKind: kind, EpochSeconds: seconds, EpochMilliseconds: instant.UnixMilli(), Formatted: local.Format(format), ISO8601: local.Format(time.RFC3339Nano)}, nil
}
