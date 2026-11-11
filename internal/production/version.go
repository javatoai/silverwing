package production

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
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

func parseBaseVersion(name string) ([4]uint64, error) {
	v, ok := parseVersion(name)
	if !ok || strings.Count(name, ".") != 2 {
		return v, errors.New("基础版本必须为三段正式数字，例如 2.3.44")
	}
	return v, nil
}

type versionReference struct {
	version [4]uint64
	label   string
}

// Remote release branches reserve their version even before a Tag is pushed.
// Both preview representations are accepted, including older partial previews.
func versionReferences(p Preview) []versionReference {
	var refs []versionReference
	for _, name := range p.Tags {
		if v, ok := parseVersion(name); ok {
			refs = append(refs, versionReference{v, "Tag " + name})
		}
	}
	branches := map[string]bool{}
	for _, name := range p.Branches {
		branches[name] = true
	}
	for name, sha := range p.BranchHeads {
		if sha != "" {
			branches[name] = true
		}
	}
	for name := range branches {
		if !strings.HasPrefix(name, "release/") {
			continue
		}
		if v, ok := parseVersion(strings.TrimPrefix(name, "release/")); ok {
			refs = append(refs, versionReference{v, "分支 " + name})
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].label < refs[j].label })
	return refs
}

func sameSeries(a, b [4]uint64) bool { return a[0] == b[0] && a[1] == b[1] }

func nextSeriesVersion(v [4]uint64, refs []versionReference) (string, error) {
	for _, ref := range refs {
		if sameSeries(v, ref.version) && compareVersion(ref.version, v) > 0 {
			v = ref.version
		}
	}
	if v[2] == math.MaxUint64 {
		return "", fmt.Errorf("%d.%d 系列的基础版本已达到上限，请手工填写更高主／次版本的三段基础版本", v[0], v[1])
	}
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]+1), nil
}

// SuggestedVersion follows the confirmed production series, never the largest
// version in an unrelated historical series. A manual baseline may be nonnumeric;
// it remains usable, but cannot supply an automatic version suggestion.
func SuggestedVersion(p Preview) (string, error) {
	v, ok := parseVersion(p.ProductionTag)
	if !ok {
		return "", errors.New("线上生产 Tag 未确认或不是三／四段正式数字，无法自动建议版本；请确认生产基线并手工填写三段基础版本")
	}
	return nextSeriesVersion(v, versionReferences(p))
}

func versionPolicyError(message string, v [4]uint64, p Preview, refs []versionReference) error {
	if current, ok := parseVersion(p.ProductionTag); ok && compareVersion(current, v) > 0 {
		v = current
	}
	suggestion, err := nextSeriesVersion(v, refs)
	if err != nil {
		return fmt.Errorf("%s；%w", message, err)
	}
	return fmt.Errorf("%s；建议使用 release/%s", message, suggestion)
}

// ValidateVersion checks a new release only. Existing batch records validate
// syntax separately and continue to use their original release and Tag family.
func ValidateVersion(name string, p Preview) error {
	v, err := parseBaseVersion(name)
	if err != nil {
		return err
	}
	refs := versionReferences(p)
	var occupied []string
	for _, ref := range refs {
		if sameSeries(v, ref.version) && v[2] == ref.version[2] {
			occupied = append(occupied, ref.label)
		}
	}
	if len(occupied) > 0 {
		return versionPolicyError(fmt.Sprintf("基础版本 %s 已被远端引用占用：%s", name, strings.Join(occupied, "、")), v, p, refs)
	}
	if current, ok := parseVersion(p.ProductionTag); ok && compareVersion(v, current) <= 0 {
		return versionPolicyError(fmt.Sprintf("基础版本 %s 必须大于已确认的线上生产 Tag %s", name, p.ProductionTag), v, p, refs)
	}
	var latest versionReference
	found := false
	for _, ref := range refs {
		if sameSeries(v, ref.version) && (!found || compareVersion(ref.version, latest.version) > 0) {
			latest, found = ref, true
		}
	}
	if found && compareVersion(v, latest.version) <= 0 {
		return versionPolicyError(fmt.Sprintf("基础版本 %s 必须高于同系列远端引用 %s", name, latest.label), v, p, refs)
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
