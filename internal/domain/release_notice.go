package domain

import (
	"fmt"
	"strings"
)

// LarkNoticeConfig contains application-scoped addresses, never credentials.
// The current user is resolved from the selected CLI profile at runtime.
type LarkNoticeConfig struct {
	Profile          string              `json:"profile"`
	AppID            string              `json:"appId"`
	Contacts         []LarkNoticeContact `json:"contacts"`
	BranchChatID     string              `json:"branchChatId,omitempty"`
	CLIDefaultTester string              `json:"cliDefaultTester,omitempty"`
}

type LarkNoticeContact struct {
	Name   string `json:"name"`
	OpenID string `json:"openId"`
}

func DefaultLarkNoticeConfig() LarkNoticeConfig {
	return LarkNoticeConfig{
		Profile: "cli_a95dcece863a9cce", AppID: "cli_a95dcece863a9cce",
		Contacts: []LarkNoticeContact{},
	}
}

func (c AppConfig) EffectiveLarkNotice() LarkNoticeConfig {
	if c.LarkNotice != nil {
		return *c.LarkNotice
	}
	return DefaultLarkNoticeConfig()
}

// Validate the profile separately from optional CLI tester mappings.
func (c LarkNoticeConfig) Validate() error {
	if strings.TrimSpace(c.Profile) == "" || strings.TrimSpace(c.AppID) == "" || strings.ContainsAny(c.Profile+c.AppID, "\r\n\x00") {
		return fmt.Errorf("Lark profile 和应用 ID 不能为空或包含控制字符")
	}
	return nil
}
