package domain

import (
	"fmt"
	"regexp"
	"strconv"
)

type VersionTag struct {
	Name                  string
	CreatedAtEpochSeconds int64
}

var TagVersionValidPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(\.\d+|\.beta-\d+)?$`)

var (
	tagBetaPattern    = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)\.beta-(\d+)$`)
	tagNumericPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)\.(\d+)$`)
	tagReleasePattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)
)

type parsedVersionTag struct {
	major, minor, patch uint64
	kind                uint8
	suffix              uint64
}

func NextTagVersion(current string) (string, error) {
	if !TagVersionValidPattern.MatchString(current) {
		return "", fmt.Errorf("不支持的测试Tag格式：%s", current)
	}
	if match := tagBetaPattern.FindStringSubmatch(current); match != nil {
		next, err := incrementVersionPart(match[4])
		if err != nil {
			return "", fmt.Errorf("无法计算下一个测试Tag：%w", err)
		}
		return fmt.Sprintf("%s.%s.%s.beta-%d", match[1], match[2], match[3], next), nil
	}
	if match := tagNumericPattern.FindStringSubmatch(current); match != nil {
		patch, err := incrementVersionPart(match[3])
		if err != nil {
			return "", fmt.Errorf("无法计算下一个测试Tag：%w", err)
		}
		return fmt.Sprintf("%s.%s.%d.beta-1", match[1], match[2], patch), nil
	}
	if match := tagReleasePattern.FindStringSubmatch(current); match != nil {
		patch, err := incrementVersionPart(match[3])
		if err != nil {
			return "", fmt.Errorf("无法计算下一个测试Tag：%w", err)
		}
		return fmt.Sprintf("%s.%s.%d.beta-1", match[1], match[2], patch), nil
	}
	return "", fmt.Errorf("无法计算下一个测试Tag：%s", current)
}

func LatestTagVersion(tags []VersionTag) string {
	type candidate struct {
		version parsedVersionTag
		tag     VersionTag
	}
	parsed := make([]candidate, 0, len(tags))
	var latestReleaseLine, latestBetaLine *parsedVersionTag
	var latestReleaseAt, latestBetaAt int64
	hasRelease, hasBeta := false, false
	for _, tag := range tags {
		version, ok := parseVersionTag(tag.Name)
		if !ok {
			continue
		}
		parsed = append(parsed, candidate{version: version, tag: tag})
		line := version
		line.patch, line.kind, line.suffix = 0, 0, 0
		if version.kind == 0 {
			if !hasBeta || tag.CreatedAtEpochSeconds > latestBetaAt {
				copy := line
				latestBetaLine, latestBetaAt, hasBeta = &copy, tag.CreatedAtEpochSeconds, true
			}
		} else if !hasRelease || tag.CreatedAtEpochSeconds > latestReleaseAt {
			copy := line
			latestReleaseLine, latestReleaseAt, hasRelease = &copy, tag.CreatedAtEpochSeconds, true
		}
	}
	if len(parsed) == 0 {
		return ""
	}
	active := latestReleaseLine
	if active == nil || (latestBetaLine != nil && compareMajorMinor(*latestBetaLine, *active) > 0) {
		active = latestBetaLine
	}
	if active == nil {
		return ""
	}
	var winner *candidate
	for i := range parsed {
		item := &parsed[i]
		if item.version.major != active.major || item.version.minor != active.minor {
			continue
		}
		if winner == nil || compareParsed(item.version, winner.version) > 0 {
			winner = item
		}
	}
	if winner == nil {
		return ""
	}
	return winner.tag.Name
}

func compareMajorMinor(a, b parsedVersionTag) int {
	if a.major < b.major {
		return -1
	}
	if a.major > b.major {
		return 1
	}
	if a.minor < b.minor {
		return -1
	}
	if a.minor > b.minor {
		return 1
	}
	return 0
}

func compareParsed(a, b parsedVersionTag) int {
	if a.major < b.major {
		return -1
	}
	if a.major > b.major {
		return 1
	}
	if a.minor < b.minor {
		return -1
	}
	if a.minor > b.minor {
		return 1
	}
	if a.patch < b.patch {
		return -1
	}
	if a.patch > b.patch {
		return 1
	}
	if a.kind < b.kind {
		return -1
	}
	if a.kind > b.kind {
		return 1
	}
	if a.suffix < b.suffix {
		return -1
	}
	if a.suffix > b.suffix {
		return 1
	}
	return 0
}

func parseVersionTag(value string) (parsedVersionTag, bool) {
	if m := tagBetaPattern.FindStringSubmatch(value); m != nil {
		parts, ok := parseVersionParts(m[1:]...)
		if !ok {
			return parsedVersionTag{}, false
		}
		return parsedVersionTag{major: parts[0], minor: parts[1], patch: parts[2], kind: 0, suffix: parts[3]}, ok
	}
	if m := tagNumericPattern.FindStringSubmatch(value); m != nil {
		parts, ok := parseVersionParts(m[1:]...)
		if !ok {
			return parsedVersionTag{}, false
		}
		return parsedVersionTag{major: parts[0], minor: parts[1], patch: parts[2], kind: 2, suffix: parts[3]}, ok
	}
	if m := tagReleasePattern.FindStringSubmatch(value); m != nil {
		parts, ok := parseVersionParts(m[1:]...)
		if !ok {
			return parsedVersionTag{}, false
		}
		return parsedVersionTag{major: parts[0], minor: parts[1], patch: parts[2], kind: 1}, ok
	}
	return parsedVersionTag{}, false
}

func parseVersionParts(values ...string) ([]uint64, bool) {
	parts := make([]uint64, len(values))
	for i, value := range values {
		// Kotlin's source contract parses every numeric component with
		// String.toLong(); values above MaxInt64 are invalid even though Go's
		// uint64 parser could represent them.
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, false
		}
		parts[i] = uint64(parsed)
	}
	return parts, true
}

func incrementVersionPart(value string) (uint64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed == int64(^uint64(0)>>1) {
		return 0, fmt.Errorf("version number overflow")
	}
	return uint64(parsed + 1), nil
}
