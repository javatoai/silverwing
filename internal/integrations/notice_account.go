package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/releasenotice"
)

type noticeAccountContext struct {
	fingerprint, profile, host string
	bound                      bool
}

// Only opaque hashes leave this function. On Windows the hash watches the
// encrypted DPAPI value and fallback file without decrypting either. It is a
// local account-change fence, not online authorization. Unsupported stores
// conservatively use user me instead of trusting an unobservable login.
func (s *Service) noticeLocalAccount(ctx context.Context, cfg domain.AppConfig) noticeAccountContext {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".meegle")
	data, err := noticeSmallFile(filepath.Join(dir, "config.json"))
	var root struct {
		Current  string                    `json:"current"`
		Profiles map[string]map[string]any `json:"profiles"`
	}
	known := err == nil && json.Unmarshal(data, &root) == nil
	profile := strings.TrimSpace(root.Current)
	if profile == "" {
		profile = "default"
	}
	values := root.Profiles[profile]
	host, _ := values["host"].(string)
	host = os.ExpandEnv(host)
	if value := os.Getenv("MEEGLE_HOST"); value != "" {
		host = value
	}
	host = strings.ToLower(strings.TrimSpace(host))
	token, _ := values["user_access_token"].(string)
	token = os.ExpandEnv(token)
	if value := os.Getenv("MEEGLE_USER_ACCESS_TOKEN"); value != "" {
		token = value
	}
	env := []string{}
	for _, value := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(value), "MEEGLE_") {
			env = append(env, value)
		}
	}
	// Config placeholders may name arbitrary environment variables.
	_ = os.Expand(string(data), func(name string) string { env = append(env, name+"="+os.Getenv(name)); return "" })
	sort.Strings(env)
	file := "credentials.enc"
	if profile != "default" {
		file = "credentials-" + profile + ".enc"
	}
	// A profile is never allowed to turn credential inspection into a path read.
	var fallback []byte
	if filepath.Base(file) == file && !strings.ContainsAny(profile, `/\`) {
		fallback, _ = noticeSmallFile(filepath.Join(dir, file))
	}
	native, observable := noticeNativeCredentialStamp(profile)
	bound := known && host != "" && (token != "" || observable && (native != "" || len(fallback) != 0))
	fingerprint := releasenotice.Digest([]any{filepath.Clean(home), s.commandPath(ctx, cfg, domain.ProxyTargetMeegle), string(data), env, native, releasenotice.Digest(fallback)})
	return noticeAccountContext{fingerprint: fingerprint, profile: profile, host: host, bound: bound}
}

func noticeSmallFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("本地 Meegle 配置不可识别")
	}
	return os.ReadFile(path)
}

func noticeAccountID(raw string) string {
	var fields []string
	if json.Unmarshal([]byte(raw), &fields) != nil || len(fields) != 8 || fields[0] != "meegle" || fields[4] == "" {
		return ""
	}
	return releasenotice.Digest(fields)
}

func (s *Service) noticeCurrentAccount(ctx context.Context, cfg domain.AppConfig, local noticeAccountContext) (string, error) {
	if local.host != "" && local.profile != "" {
		result, err := s.runMeegle(ctx, cfg, []string{"user", "me", "--format", "json"}, 15*time.Second, "核对通知 Meegle 账号")
		if err != nil {
			return "", err
		}
		value, err := decodeJSON(result.Stdout)
		user := object(value)
		if err != nil || text(user["user_key"]) == "" {
			return "", errors.New("无法确认当前 Meegle 账号")
		}
		fields := []string{"meegle", s.commandPath(ctx, cfg, domain.ProxyTargetMeegle), local.profile, local.host, text(user["user_key"])}
		for _, name := range []string{"tenant_key", "tenant_id", "enterprise_id"} {
			fields = append(fields, text(user[name]))
		}
		return releasenotice.Digest(fields), nil
	}
	raw, loggedOut, err := s.requirementCacheIdentity(ctx, cfg)
	if err == nil && (loggedOut || raw == "") {
		err = errors.New("无法确认当前 Meegle 账号")
	}
	return noticeAccountID(raw), err
}
