// Package larkcli adapts the installed CLI. It never reads credential files.
package larkcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
	"silverwing-go/internal/releasenotice"
)

type Runner interface {
	Run(context.Context, process.Spec) (process.Result, error)
}
type Client struct {
	Runner Runner
	Config domain.AppConfig
}

func (c Client) Run(ctx context.Context, args []string, timeout time.Duration) (process.Result, error) {
	command := ""
	if c.Config.LarkExecutablePath != nil {
		command = strings.TrimSpace(*c.Config.LarkExecutablePath)
	}
	// nil proxy policy deliberately preserves only the CLI's inherited environment.
	spec, e := commandruntime.Prepare(ctx, c.Runner, nil, "", executable.ToolLark, command)
	if e != nil {
		return process.Result{}, preparationError{e}
	}
	spec.Executable, e = nativeCommand(spec.Executable)
	if e != nil {
		return process.Result{}, preparationError{e}
	}
	spec.Arguments = args
	spec.Timeout = timeout
	if spec.Env == nil {
		spec.Env = map[string]string{}
	}
	spec.Env["LARKSUITE_CLI_NO_UPDATE_NOTIFIER"] = "1"
	spec.Env["LARKSUITE_CLI_NO_SKILLS_NOTIFIER"] = "1"
	return c.Runner.Run(ctx, spec)
}
func (c Client) Identity(ctx context.Context) (releasenotice.Identity, error) {
	settings := c.Config.EffectiveLarkNotice()
	r, e := c.Run(ctx, []string{"auth", "status", "--profile", settings.Profile, "--json"}, 15*time.Second)
	if e != nil {
		return releasenotice.Identity{}, fmt.Errorf("无法读取 Lark 本地身份：%w", e)
	}
	if r.ExitCode != 0 {
		return releasenotice.Identity{}, errors.New("Lark 本地身份读取失败，请检查 CLI 应用配置")
	}
	data := []byte(r.Stdout)
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) == nil && len(envelope.Data) > 0 {
		data = envelope.Data
	}
	var state struct {
		AppID      string `json:"appId"`
		Identities struct {
			User struct {
				UserName    string `json:"userName"`
				TokenStatus string `json:"tokenStatus"`
				OpenID      string `json:"openId"`
			} `json:"user"`
			Bot struct {
				Status string `json:"status"`
			} `json:"bot"`
		} `json:"identities"`
	}
	if json.Unmarshal(data, &state) != nil || state.AppID == "" {
		return releasenotice.Identity{}, errors.New("Lark 身份 JSON 响应无效")
	}
	if state.AppID != settings.AppID {
		return releasenotice.Identity{}, errors.New("Lark profile 的应用与通知应用配置不匹配")
	}
	// Cached identity remains usable when the personal OAuth token expires.
	return releasenotice.Identity{AppID: state.AppID, Name: strings.TrimSpace(state.Identities.User.UserName), OpenID: strings.TrimSpace(state.Identities.User.OpenID), BotReady: state.Identities.Bot.Status == "ready", UserState: state.Identities.User.TokenStatus}, nil
}
func (c Client) Send(ctx context.Context, to, card, key string) releasenotice.SendResult {
	if !releasenotice.ValidOpenID(to) {
		return releasenotice.SendResult{Error: "接收人 open_id 无效"}
	}
	cfg := c.Config.EffectiveLarkNotice()
	r, e := c.Run(ctx, []string{"im", "+messages-send", "--profile", cfg.Profile, "--as", "bot", "--user-id", to, "--msg-type", "interactive", "--content", card, "--idempotency-key", key}, time.Minute)
	if e != nil {
		var preparation preparationError
		if errors.As(e, &preparation) {
			return releasenotice.SendResult{Error: "发送前检查失败：" + commandruntime.Redact(c.Config, e.Error())}
		}
		return releasenotice.SendResult{Error: "发送结果待核实：" + commandruntime.Redact(c.Config, e.Error()), Unknown: true}
	}
	var response struct {
		OK   bool `json:"ok"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
		Error struct {
			Code    json.RawMessage `json:"code"`
			Message string          `json:"message"`
			Type    string          `json:"type"`
		} `json:"error"`
	}
	raw := r.Stdout
	if r.ExitCode != 0 {
		raw = r.Stderr
	}
	if json.Unmarshal([]byte(raw), &response) != nil {
		return releasenotice.SendResult{Error: "发送响应无法确认，请使用原幂等键重试", Unknown: true}
	}
	if r.ExitCode == 0 && response.OK && response.Data.MessageID != "" {
		return releasenotice.SendResult{MessageID: response.Data.MessageID}
	}
	message := strings.TrimSpace(response.Error.Message)
	if strings.Contains(string(response.Error.Code), "230013") || strings.Contains(message, "Bot has NO availability") {
		message = "机器人对该接收人不可用，请检查应用可用范围、禁用范围及配置是否已发布生效"
	}

	if message == "" {
		message = "发送结果待核实，未取得有效的 message_id"
	}
	return releasenotice.SendResult{Error: commandruntime.Redact(c.Config, message), Unknown: len(response.Error.Code) == 0}
}
