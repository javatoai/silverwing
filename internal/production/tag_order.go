package production

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// Git's creatordate uses the tagger date for annotated Tags and the commit
// date for lightweight Tags. Only refs matching the observed remote object are
// eligible: unrelated local Tags and stale objects cannot affect the picker.
func (s *Service) tagsByCreation(ctx context.Context, t Target, refs references) ([]string, error) {
	names := tagNames(refs)
	prefix := namespace(t) + "/tags/"
	out, err := s.run(ctx, t.Repository, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(creatordate:unix)", prefix)
	if err != nil {
		return names, err
	}
	dates := map[string]int64{}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(strings.TrimSuffix(line, "\r"), "\x00")
		if len(parts) != 3 || !strings.HasPrefix(parts[0], prefix) {
			continue
		}
		name := strings.TrimPrefix(parts[0], prefix)
		if refs["refs/tags/"+name] != parts[1] {
			continue
		}
		if date, e := strconv.ParseInt(parts[2], 10, 64); e == nil {
			dates[name] = date
		}
	}
	sort.Slice(names, func(i, j int) bool {
		a, aOK := dates[names[i]]
		b, bOK := dates[names[j]]
		if aOK != bOK {
			return aOK
		}
		if a != b {
			return a > b
		}
		return names[i] < names[j]
	})
	return names, nil
}
