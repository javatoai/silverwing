package domain

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

const (
	ManagedDocumentsDirectory         = "managed-documents"
	ManagedDocumentsRepositoryIDSize  = 16
	TaskMaterialsRepositoryRole       = "task-materials"
	ProjectInstructionsRepositoryRole = "project-instructions"
	ProjectInstructionsDirectory      = "project-materials"
)

func ManagedTaskMaterialsRoot(home string, settings MaterialsGitSyncSettings) string {
	return managedDocumentsRoot(home, TaskMaterialsRepositoryRole, settings.RepositoryURL)
}

func ManagedProjectInstructionsRoot(home, repositoryURL string) string {
	return managedDocumentsRoot(home, ProjectInstructionsRepositoryRole, repositoryURL)
}

func managedDocumentsRoot(home, role, repositoryURL string) string {
	if strings.TrimSpace(home) == "" || strings.TrimSpace(repositoryURL) == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(materialsGitURLIdentity(repositoryURL)))
	return filepath.Join(home, ManagedDocumentsDirectory, role, fmt.Sprintf("%x", digest[:ManagedDocumentsRepositoryIDSize]))
}

func materialsGitURLIdentity(address string) string {
	path := strings.TrimSuffix(address, "/")
	base := strings.TrimSuffix(path, ".git")
	if strings.HasSuffix(base, "/") {
		return address
	}
	return base + address[len(path):]
}

// MaterialsGitSyncSettings belongs to integrations.json, independently of the
// service repositories and their protected write branches.
type MaterialsGitSyncSettings struct {
	Enabled         bool   `json:"enabled"`
	RepositoryURL   string `json:"repositoryUrl"`
	Branch          string `json:"branch"`
	IntervalMinutes int    `json:"intervalMinutes"`
	LocalRoot       string `json:"localRoot,omitempty"`
	RemoteName      string `json:"remoteName,omitempty"`
	BindingID       string `json:"bindingId,omitempty"`
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

func (c AppConfig) ProjectInstructionsGitSettings() MaterialsGitSyncSettings {
	settings := MaterialsGitSyncSettings{
		Enabled:         c.ProjectInstructionsGitURL != "",
		RepositoryURL:   c.ProjectInstructionsGitURL,
		Branch:          c.ProjectInstructionsGitBranch,
		IntervalMinutes: DefaultMaterialsGitSyncSettings().IntervalMinutes,
		LocalRoot:       c.ProjectInstructionsGitLocalRoot,
		RemoteName:      c.ProjectInstructionsGitRemoteName,
		BindingID:       c.ProjectInstructionsGitBindingID,
	}
	if c.ProjectInstructionsGitEnabled != nil && settings.RepositoryURL != "" {
		settings.Enabled = *c.ProjectInstructionsGitEnabled
	}
	if c.ProjectInstructionsGitIntervalMinutes != nil {
		settings.IntervalMinutes = *c.ProjectInstructionsGitIntervalMinutes
	}
	return settings
}

func TaskMaterialsRepositoryRoot(home string, settings MaterialsGitSyncSettings) string {
	if settings.LocalRoot != "" {
		return settings.LocalRoot
	}
	return ManagedTaskMaterialsRoot(home, settings)
}

func ProjectInstructionsRepositoryRoot(home string, settings MaterialsGitSyncSettings) string {
	if settings.LocalRoot != "" {
		return settings.LocalRoot
	}
	return ManagedProjectInstructionsRoot(home, settings.RepositoryURL)
}

// SameMaterialsGitRepositoryURL compares the saved target independently of
// the address used for Git transport. Only the optional .git suffix may vary.
func SameMaterialsGitRepositoryURL(a, b string) bool {
	if a == b {
		return true
	}
	return materialsGitURLIdentity(a) == materialsGitURLIdentity(b) && ValidateRemoteGitURL(a) == nil && ValidateRemoteGitURL(b) == nil
}

// ValidateSynchronizationGitURL accepts an empty value to disable a repository.
func ValidateSynchronizationGitURL(address string) error {
	if address == "" {
		return nil
	}
	if parsed, err := url.Parse(address); err == nil && parsed.User != nil {
		if _, password := parsed.User.Password(); password {
			return fmt.Errorf("Git 地址不能包含密码，请使用系统 Git 凭据")
		}
	}
	return ValidateRemoteGitURL(address)
}

func (s MaterialsGitSyncSettings) Validate() error {
	return s.validate(false)
}

func (s MaterialsGitSyncSettings) validate(allowDefaultBranch bool) error {
	if err := s.validateBranchAndInterval(allowDefaultBranch && s.LocalRoot == ""); err != nil {
		return err
	}
	if s.LocalRoot != "" || s.RemoteName != "" || s.BindingID != "" {
		if !filepath.IsAbs(s.LocalRoot) || filepath.Clean(s.LocalRoot) != s.LocalRoot || filepath.Dir(s.LocalRoot) == s.LocalRoot ||
			s.RemoteName == "" || strings.HasPrefix(s.RemoteName, "-") || ValidateGitRef(&s.RemoteName) != nil ||
			len(s.BindingID) != 32 || strings.IndexFunc(s.BindingID, func(r rune) bool { return !strings.ContainsRune("0123456789abcdef", r) }) != -1 {
			return fmt.Errorf("本地 Git 仓库绑定信息不完整，请重新选择仓库目录")
		}
		return ValidateLocalDocumentsGitURL(s.RepositoryURL)
	}
	if s.Enabled && s.RepositoryURL == "" {
		return ValidateRemoteGitURL(s.RepositoryURL)
	}
	return ValidateSynchronizationGitURL(s.RepositoryURL)
}

// Existing clones may refer to an absolute filesystem remote in addition to a
// network remote. Never reinterpret a relative address from a different cwd.
func ValidateLocalDocumentsGitURL(address string) error {
	if filepath.IsAbs(address) && filepath.Clean(address) == address && !strings.ContainsAny(address, "\x00\r\n") {
		return nil
	}
	if address == "" {
		return fmt.Errorf("本地 Git 仓库没有远端地址")
	}
	return ValidateSynchronizationGitURL(address)
}

func (s MaterialsGitSyncSettings) validateBranchAndInterval(allowDefaultBranch bool) error {
	if s.IntervalMinutes <= 0 || int64(s.IntervalMinutes) > int64((1<<63-1)/time.Minute) {
		return fmt.Errorf("Git 同步周期必须为有效的正整数分钟")
	}
	if allowDefaultBranch && s.Branch == "" {
		return nil
	}
	if s.Branch == "" || strings.HasPrefix(s.Branch, "-") || strings.HasPrefix(s.Branch, "refs/") || ValidateGitRef(&s.Branch) != nil {
		return fmt.Errorf("Git 同步分支不合法")
	}
	return nil
}
