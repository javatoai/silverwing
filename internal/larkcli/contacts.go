package larkcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"silverwing-go/internal/releasenotice"
)

// ResolveTester accepts an explicit application-scoped mapping, or one complete
// exact-name/alias search result. A partial/ambiguous directory response is not a recipient.
func (c Client) ResolveTester(ctx context.Context, name string) (releasenotice.Recipient, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return releasenotice.Recipient{}, errors.New("测试负责人姓名为空")
	}
	mapped := map[string]bool{}
	for _, p := range c.Config.EffectiveLarkNotice().Contacts {
		if strings.Join(strings.Fields(p.Name), " ") == name {
			mapped[p.OpenID] = true
		}
	}
	if len(mapped) > 0 {
		if len(mapped) != 1 {
			return releasenotice.Recipient{}, fmt.Errorf("%s 的联系人配置存在歧义", name)
		}
		for id := range mapped {
			if !releasenotice.ValidOpenID(id) {
				return releasenotice.Recipient{}, fmt.Errorf("%s 的联系人 open_id 无效", name)
			}
			return releasenotice.Recipient{Name: name, OpenID: id}, nil
		}
	}
	if !c.AsUser {
		return releasenotice.Recipient{}, errors.New("查询测试联系人需要用户身份")
	}
	result, err := c.Run(ctx, []string{"contact", "+search-user", "--profile", c.Config.EffectiveLarkNotice().Profile, "--as", "user", "--query", name, "--exclude-external-users", "--json"}, 30*time.Second)
	if err != nil {
		return releasenotice.Recipient{}, fmt.Errorf("无法查询测试联系人 %s", name)
	}
	var response struct {
		OK   bool `json:"ok"`
		Data struct {
			HasMore bool `json:"has_more"`
			Users   []struct {
				OpenID   string   `json:"open_id"`
				Name     string   `json:"localized_name"`
				Active   bool     `json:"is_activated"`
				External bool     `json:"is_cross_tenant"`
				Aliases  []string `json:"match_segments"`
			} `json:"users"`
		} `json:"data"`
	}
	if result.ExitCode != 0 || result.StdoutTruncated || json.Unmarshal([]byte(result.Stdout), &response) != nil || !response.OK || response.Data.HasMore {
		return releasenotice.Recipient{}, fmt.Errorf("未取得 %s 的完整通讯录结果", name)
	}
	matches := map[string]bool{}
	for _, u := range response.Data.Users {
		if !u.Active || u.External || !releasenotice.ValidOpenID(u.OpenID) {
			continue
		}
		labels := append([]string{u.Name}, u.Aliases...)
		for _, label := range labels {
			if strings.Join(strings.Fields(label), " ") == name {
				matches[u.OpenID] = true
			}
		}
	}
	if len(matches) != 1 {
		return releasenotice.Recipient{}, fmt.Errorf("无法唯一确定测试联系人 %s", name)
	}
	for id := range matches {
		return releasenotice.Recipient{Name: name, OpenID: id}, nil
	}
	return releasenotice.Recipient{}, errors.New("测试联系人不存在")
}
