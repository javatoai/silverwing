package platform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"silverwing-go/internal/storage"
	"silverwing-go/skills"
)

type CLIBundleInstallResult struct {
	Path        string `json:"path"`
	Replacement string `json:"replacement,omitempty"`
	RecordError string `json:"recordError,omitempty"`
}

type CLIBundleStatus struct {
	CLIPath   string `json:"cliPath,omitempty"`
	SkillPath string `json:"skillPath,omitempty"`
}

func (s *Service) cliBundleStatus(ctx context.Context) (CLIBundleStatus, error) {
	var status CLIBundleStatus
	if err := ctx.Err(); err != nil {
		return status, err
	}
	if path, err := readBounded(s.cliBundlePathRecord(), 32<<10); err == nil && isInstalledSW(string(path)) {
		status.CLIPath = string(path)
	} else if path, err := exec.LookPath("sw"); err == nil && isInstalledSW(path) {
		status.CLIPath = path
	}
	if path, err := cliBundleSkillPath(); err == nil {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			status.SkillPath = path
		}
	}
	return status, ctx.Err()
}

func isInstalledSW(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		return false
	}
	// Verify identity without launching an arbitrary command found on PATH.
	build, err := buildinfo.ReadFile(path)
	return err == nil && build.Path == "silverwing-go/cmd/sw"
}

func (s *Service) cliBundlePathRecord() string {
	return filepath.Join(s.paths.Home, "sw-installed-path")
}

func cliBundleSkillPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agents", "skills", "silverwing-tag", "SKILL.md"), nil
}

func (s *Service) installCLIBundle(ctx context.Context, payload map[string]any) (CLIBundleInstallResult, error) {
	if err := requireConfirmed(payload, "安装 Silverwing 工具"); err != nil {
		return CLIBundleInstallResult{}, err
	}
	var path string
	var data []byte
	var mode os.FileMode
	artifact := payloadString(payload, "artifact")
	switch artifact {
	case "cli":
		dir := payloadString(payload, "path")
		if !filepath.IsAbs(dir) || !isDir(dir) {
			return CLIBundleInstallResult{}, errors.New("请选择可用的 CLI 安装目录")
		}
		executable, err := os.Executable()
		if err != nil {
			return CLIBundleInstallResult{}, err
		}
		name := "sw"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		data, err = readBounded(filepath.Join(filepath.Dir(executable), name), 64<<20)
		if err != nil {
			return CLIBundleInstallResult{}, fmt.Errorf("无法读取安装包随附的 %s：%w", name, err)
		}
		path, mode = filepath.Join(dir, name), 0o755
	case "skill":
		target, err := cliBundleSkillPath()
		if err != nil {
			return CLIBundleInstallResult{}, err
		}
		path, mode = target, 0o644
		data = []byte(skills.SilverwingTag)
	default:
		return CLIBundleInstallResult{}, errors.New("无法识别要安装的 Silverwing 工具")
	}
	result, err := s.installCLIBundleFile(ctx, path, data, mode, payloadString(payload, "expectedReplacement"))
	if err == nil && result.Replacement == "" && artifact == "cli" {
		record := s.cliBundlePathRecord()
		recordErr := ensureNoExistingSymlinks(record)
		if recordErr == nil {
			recordErr = storage.WriteFileAtomic(record, []byte(result.Path), 0o600)
		}
		if recordErr != nil {
			result.RecordError = "已安装，无法记录安装路径"
		}
	}
	return result, err
}

func (s *Service) installCLIBundleFile(ctx context.Context, path string, data []byte, mode os.FileMode, expected string) (CLIBundleInstallResult, error) {
	if len(data) == 0 {
		return CLIBundleInstallResult{}, errors.New("安装文件为空")
	}
	if err := ctx.Err(); err != nil {
		return CLIBundleInstallResult{}, err
	}
	if err := ensureNoExistingSymlinks(path); err != nil {
		return CLIBundleInstallResult{}, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return CLIBundleInstallResult{}, err
	}
	path = absolute
	lockPath := path
	if runtime.GOOS == "windows" {
		lockPath = strings.ToLower(lockPath)
	}
	lockID := sha256.Sum256([]byte(lockPath))
	unlock, err := storage.AcquireLock(filepath.Join(s.paths.Locks, fmt.Sprintf("cli-bundle-%x.lock", lockID[:12])))
	if err != nil {
		return CLIBundleInstallResult{}, err
	}
	defer unlock()
	if err := ensureNoExistingSymlinks(path); err != nil {
		return CLIBundleInstallResult{}, err
	}
	result := CLIBundleInstallResult{Path: path}
	existing, err := readBounded(path, 64<<20)
	if err != nil && !os.IsNotExist(err) {
		return CLIBundleInstallResult{}, err
	}
	if err == nil && bytes.Equal(existing, data) {
		if runtime.GOOS != "windows" && mode&0o111 != 0 {
			info, err := os.Lstat(path)
			if err != nil {
				return result, err
			}
			if info.Mode().Perm()&0o100 == 0 {
				return result, os.Chmod(path, info.Mode().Perm()|0o100)
			}
		}
		return result, nil
	}
	replacement := ""
	if err == nil {
		binding := fmt.Sprintf("%s\x00%x\x00%x", lockPath, sha256.Sum256(existing), sha256.Sum256(data))
		replacement = fmt.Sprintf("%x", sha256.Sum256([]byte(binding)))
	}
	if expected != "" && expected != replacement {
		return CLIBundleInstallResult{}, errors.New("安装文件或目标已变化，请重新确认")
	}
	if replacement != "" && expected == "" {
		result.Replacement = replacement
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return CLIBundleInstallResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return CLIBundleInstallResult{}, err
	}
	if err := ensureNoExistingSymlinks(path); err != nil {
		return CLIBundleInstallResult{}, err
	}
	return result, storage.WriteFileAtomic(path, data, mode)
}
