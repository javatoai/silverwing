package domain

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// MaterialsGitSyncSettings belongs to integrations.json, independently of the
// service repositories and their protected write branches.
type MaterialsGitSyncSettings struct {
	Enabled         bool   `json:"enabled"`
	RepositoryURL   string `json:"repositoryUrl"`
	Branch          string `json:"branch"`
	IntervalMinutes int    `json:"intervalMinutes"`
}

func DefaultMaterialsGitSyncSettings() MaterialsGitSyncSettings {
	return MaterialsGitSyncSettings{Branch: "master", IntervalMinutes: 5}
}

func (c AppConfig) MaterialsGitSettings() MaterialsGitSyncSettings {
	if c.MaterialsGitSync == nil {
		return DefaultMaterialsGitSyncSettings()
	}
	return *c.MaterialsGitSync
}

func (s MaterialsGitSyncSettings) Validate() error {
	if s.IntervalMinutes <= 0 || int64(s.IntervalMinutes) > int64((1<<63-1)/time.Minute) {
		return fmt.Errorf("Git 同步周期必须为有效的正整数分钟")
	}
	if s.Branch == "" || strings.HasPrefix(s.Branch, "-") || strings.HasPrefix(s.Branch, "refs/") || ValidateGitRef(&s.Branch) != nil {
		return fmt.Errorf("Git 同步分支不合法")
	}
	if s.RepositoryURL != "" || s.Enabled {
		if parsed, err := url.Parse(s.RepositoryURL); err == nil && parsed.User != nil {
			if _, password := parsed.User.Password(); password {
				return fmt.Errorf("Git 地址不能包含密码，请使用系统 Git 凭据")
			}
		}
		if err := ValidateRemoteGitURL(s.RepositoryURL); err != nil {
			return err
		}
	}
	return nil
}
