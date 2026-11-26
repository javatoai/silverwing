package domain

import (
	"strconv"
	"strings"
)

// CompareReleaseVersions accepts stable, numeric major.minor.patch versions.
func CompareReleaseVersions(left, right string) (int, bool) {
	a, ok := releaseVersion(left)
	if !ok {
		return 0, false
	}
	b, ok := releaseVersion(right)
	if !ok {
		return 0, false
	}
	for i := range a {
		if a[i] < b[i] {
			return -1, true
		}
		if a[i] > b[i] {
			return 1, true
		}
	}
	return 0, true
}

func releaseVersion(value string) ([3]uint64, bool) {
	var numbers [3]uint64
	parts := strings.Split(value, ".")
	if len(parts) != len(numbers) {
		return numbers, false
	}
	for i, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return numbers, false
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return numbers, false
			}
		}
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return numbers, false
		}
		numbers[i] = number
	}
	return numbers, true
}
