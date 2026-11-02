package platform

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
)

// SkillInstalledOrigin records an observed installation, never current settings
// substituted for an unknown historical origin.
type SkillInstalledOrigin struct {
	RepositoryURL string `json:"repositoryUrl"`
	Ref           string `json:"ref"`
	Commit        string `json:"commit"`
}

func skillSourceRef(source domain.SkillSource) string {
	if source.Ref == nil {
		return ""
	}
	return strings.TrimSpace(*source.Ref)
}

func skillSourceFingerprint(source domain.SkillSource) string {
	root := ""
	if source.SkillRoot != nil {
		root = strings.ReplaceAll(strings.TrimSpace(*source.SkillRoot), `\`, "/")
	}
	data, _ := json.Marshal(struct{ RepositoryURL, Ref, SkillRoot string }{
		strings.TrimSpace(source.RepositoryURL), skillSourceRef(source), root,
	})
	return hashBytes(data)
}

func skillFingerprintExpectation(payload map[string]any) ([]string, error) {
	value, provided := payload["sourceFingerprint"]
	if !provided {
		return nil, nil
	}
	fingerprint, ok := value.(string)
	if !ok || fingerprint == "" {
		return nil, fmt.Errorf("Skill 来源指纹无效，请刷新来源后重试")
	}
	return []string{fingerprint}, nil
}

func validSkillCommit(commit string) bool {
	if len(commit) != 40 && len(commit) != 64 {
		return false
	}
	_, err := hex.DecodeString(commit)
	return err == nil
}

func validateSkillSourceSnapshot(source domain.SkillSource, status SkillSourceSnapshot, expected ...string) error {
	if status.SourceID != source.ID || status.SourceFingerprint == nil || *status.SourceFingerprint != skillSourceFingerprint(source) {
		return fmt.Errorf("Skill 来源配置或旧缓存已变更，请成功刷新来源后重试")
	}
	if len(expected) > 0 && expected[0] != *status.SourceFingerprint {
		return fmt.Errorf("Skill 来源已变更，请重新加载列表后重试")
	}
	if status.Error != nil {
		return fmt.Errorf("Skill 来源上次刷新失败，请成功刷新后再操作")
	}
	origin := status.ResolvedOrigin
	if origin == nil || origin.RepositoryURL != source.RepositoryURL || origin.Ref == "" || !validSkillCommit(origin.Commit) {
		return fmt.Errorf("Skill 来源尚无已验证版本，请成功刷新后重试")
	}
	if ref := skillSourceRef(source); ref != "" && ref != origin.Ref {
		return fmt.Errorf("Skill 来源版本与缓存不匹配，请成功刷新后重试")
	}
	return nil
}

func (s *Service) currentSkillSourceConfig(source domain.SkillSource) (domain.AppConfig, error) {
	cfg, err := s.loadConfig()
	if err != nil {
		return domain.AppConfig{}, err
	}
	current, ok := findSkillSource(cfg.SkillSources, source.ID)
	if !ok || skillSourceFingerprint(current) != skillSourceFingerprint(source) {
		return domain.AppConfig{}, fmt.Errorf("Skill 来源已移除或配置已变更，请重新加载来源")
	}
	return cfg, nil
}

func (s *Service) skillCheckoutPath(source domain.SkillSource) (string, error) {
	if err := source.Validate(); err != nil {
		return "", err
	}
	root, err := filepath.Abs(filepath.Join(s.paths.Codex, skillSourcesName))
	if err != nil {
		return "", err
	}
	checkout := filepath.Join(root, source.ID, skillSourceFingerprint(source))
	if !within(root, checkout) || samePath(root, checkout) {
		return "", fmt.Errorf("Skill 缓存路径不安全")
	}
	if err := ensureNoExistingSymlinks(checkout); err != nil {
		return "", err
	}
	return checkout, nil
}

func installedSkillOrigin(state extensionState, name string) *SkillInstalledOrigin {
	for _, entry := range state.ManagedSkills {
		if entry.Name == name && entry.InstalledOrigin != nil {
			origin := *entry.InstalledOrigin
			return &origin
		}
	}
	return nil
}
