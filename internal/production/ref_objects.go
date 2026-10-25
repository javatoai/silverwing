package production

import (
	"context"
	"fmt"
	"strings"
)

// Reference names and SHAs come from one remote observation. Fetch only objects
// needed by this operation, and never replace user branches or local Tags.
func (s *Service) fetchMissing(ctx context.Context, t Target, refs references, names ...string) error {
	missing := []string{}
	unique := []string{}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		sha := refs[name]
		if sha == "" {
			return fmt.Errorf("远端引用不存在：%s", name)
		}
		unique = append(unique, name)
	}
	if len(unique) == 0 {
		return ctx.Err()
	}
	var input strings.Builder
	for _, name := range unique {
		input.WriteString(refs[name] + "\n")
	}
	out, err := s.gitInput(ctx, t, input.String(), "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != len(unique) {
		return fmt.Errorf("无法读取生产发布所需的本地 Git 对象")
	}
	for n, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != refs[unique[n]] {
			return fmt.Errorf("本地 Git 对象查询结果与请求不匹配")
		}
		if fields[1] == "missing" {
			missing = append(missing, unique[n])
		}
	}
	if len(missing) == 0 {
		return ctx.Err()
	}
	args := []string{"fetch", "--no-tags", t.Remote}
	argumentBytes := 0
	for _, name := range missing {
		refspec := "+" + name + ":" + namespace(t) + "/" + strings.TrimPrefix(name, "refs/")
		// Large Tag pickers must also fit Windows' command-line length limit.
		if argumentBytes+len(refspec) > 16000 && len(args) > 3 {
			if _, err := s.run(ctx, t.Repository, args...); err != nil {
				return err
			}
			args, argumentBytes = args[:3], 0
		}
		args = append(args, refspec)
		argumentBytes += len(refspec) + 1
	}
	if _, err := s.run(ctx, t.Repository, args...); err != nil {
		return err
	}
	out, err = s.run(ctx, t.Repository, "for-each-ref", "--format=%(refname)%00%(objectname)", namespace(t)+"/")
	if err != nil {
		return err
	}
	fetched := references{}
	for _, line := range strings.Split(out, "\n") {
		if parts := strings.Split(strings.TrimSuffix(line, "\r"), "\x00"); len(parts) == 2 {
			fetched[parts[0]] = parts[1]
		}
	}
	for _, name := range missing {
		if fetched[namespace(t)+"/"+strings.TrimPrefix(name, "refs/")] != refs[name] {
			return fmt.Errorf("远端 %s 在读取期间发生变化，请重新检查", name)
		}
	}
	return nil
}

// Date ordering needs local refs, including lightweight Tags whose objects were
// already present. Update only our private cache, without another remote fetch.
func (s *Service) retainTagSnapshot(ctx context.Context, t Target, refs references) error {
	var input strings.Builder
	for _, name := range tagNames(refs) {
		input.WriteString("update " + namespace(t) + "/tags/" + name + " " + refs["refs/tags/"+name] + "\n")
	}
	if input.Len() == 0 {
		return nil
	}
	_, err := s.gitInput(ctx, t, input.String(), "update-ref", "--stdin")
	return err
}
