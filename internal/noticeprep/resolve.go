package noticeprep

import (
	"context"
	"strings"
	"sync"

	"silverwing-go/internal/releasenotice"
)

// Resolve preserves roster order and deduplicates by open_id. Any failed or
// ambiguous member rejects the entire roster; callers implement self fallback.
func Resolve(ctx context.Context, names []string, resolve func(context.Context, string) (releasenotice.Recipient, error)) ([]releasenotice.Recipient, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if len(names) == 0 {
		return nil, "需求未配置测试负责人", nil
	}
	type result struct {
		person releasenotice.Recipient
		err    error
	}
	results := make([]result, len(names))
	jobs := make(chan int, len(names))
	for i := range names {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for range min(4, len(names)) {
		workers.Go(func() {
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				results[i].person, results[i].err = resolve(ctx, names[i])
			}
		})
	}
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	out, seen := []releasenotice.Recipient{}, map[string]bool{}
	for _, result := range results {
		if result.err != nil {
			return nil, result.err.Error(), nil
		}
		if !releasenotice.ValidOpenID(result.person.OpenID) {
			return nil, "测试负责人 open_id 无效", nil
		}
		if !seen[result.person.OpenID] {
			out = append(out, result.person)
			seen[result.person.OpenID] = true
		}
	}
	return out, "", nil
}

func Names(names []string) []string {
	out, seen := []string{}, map[string]bool{}
	for _, name := range names {
		name = strings.Join(strings.Fields(name), " ")
		if name != "" && !seen[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	return out
}
