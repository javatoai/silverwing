package production

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

var formalVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:\.(0|[1-9][0-9]*))?$`)

func parseVersion(name string) ([4]uint64, bool) {
	var v [4]uint64
	m := formalVersion.FindStringSubmatch(name)
	if m == nil {
		return v, false
	}
	for i := range v {
		if m[i+1] == "" {
			continue
		}
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func compareVersion(a, b [4]uint64) int {
	for i := range a {
		if a[i] > b[i] {
			return 1
		}
		if a[i] < b[i] {
			return -1
		}
	}
	return 0
}

func latestVersion(tags []string) ([4]uint64, bool) {
	var latest [4]uint64
	found := false
	for _, name := range tags {
		v, ok := parseVersion(name)
		if ok && (!found || compareVersion(v, latest) > 0) {
			latest, found = v, true
		}
	}
	return latest, found
}

func SuggestedVersion(tags []string) (string, error) {
	v, ok := latestVersion(tags)
	if !ok {
		return "", errors.New("没有正式数字 Tag，请手工填写三段基础版本")
	}
	if v[2] == math.MaxUint64 {
		return "", errors.New("生产版本号已达到上限")
	}
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]+1), nil
}

func ValidateVersion(name string, tags []string) error {
	v, ok := parseVersion(name)
	if !ok || len(formalVersion.FindStringSubmatch(name)[4]) != 0 {
		return errors.New("基础版本必须为三段正式数字，例如 2.3.44")
	}
	if latest, found := latestVersion(tags); found && compareVersion(v, latest) <= 0 {
		return errors.New("基础版本必须大于远端已有正式版本")
	}
	return nil
}

func NextTag(version string, tags []string, first bool) (string, error) {
	base, ok := parseVersion(version)
	if !ok {
		return "", errors.New("生产基础版本无效")
	}
	var suffix uint64
	baseExists := false
	familyExists := false
	for _, name := range tags {
		if name == version {
			baseExists = true
		}
		v, valid := parseVersion(name)
		if valid && v[0] == base[0] && v[1] == base[1] && v[2] == base[2] {
			familyExists = true
		}
		if valid && v[0] == base[0] && v[1] == base[1] && v[2] == base[2] && v[3] > suffix {
			suffix = v[3]
		}
	}
	if first {
		if baseExists || familyExists {
			return "", errors.New("基础 Tag 已被占用，请结束该批次并选择新的基础版本")
		}
		return version, nil
	}
	if suffix == math.MaxUint64 {
		return "", errors.New("生产版本序号已达到上限")
	}
	return fmt.Sprintf("%s.%d", version, suffix+1), nil
}
