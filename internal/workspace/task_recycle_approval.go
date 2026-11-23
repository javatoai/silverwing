package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
)

type taskRecycleWorkspaceState struct {
	Workspace domain.ServiceWorkspace
	Identity  string
	Status    string
	CloneRefs string
}

// The approval covers the task incarnation and the actual repositories the
// confirmation described, including workspace directories that were absent.
func (l *WorkspaceLifecycle) taskRecycleGuard(ctx context.Context, taskPath string, manifest domain.TaskManifest) (string, error) {
	taskIdentity, err := documents.DirectoryFingerprint(taskPath)
	if err != nil {
		return "", err
	}
	workspaces := physicalWorkspaces(manifest)
	states := make([]taskRecycleWorkspaceState, 0, len(workspaces))
	for _, ws := range workspaces {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		target, err := l.validateTarget(taskPath, ws)
		if err != nil {
			return "", err
		}
		state := taskRecycleWorkspaceState{Workspace: ws}
		state.Identity, err = documents.DirectoryFingerprint(target)
		if os.IsNotExist(err) {
			states = append(states, state)
			continue
		}
		if err != nil {
			return "", err
		}
		// Porcelain v2 includes HEAD and the staged object IDs, so replacing an
		// index entry without changing the file name also invalidates approval.
		state.Status, err = recycleWorkspaceStatus(ctx, l.Git, target)
		if err != nil {
			return "", err
		}
		if ws.Strategy == domain.WorkspaceIndependentClone {
			state.CloneRefs, err = recycleCloneRefs(ctx, l.Git, target)
			if err != nil {
				return "", err
			}
		}
		states = append(states, state)
	}
	data, err := json.Marshal(struct {
		TaskIdentity string
		CreatedAt    string
		States       []taskRecycleWorkspaceState
	}{taskIdentity, manifest.CreatedAt, states})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func recycleCloneRefs(ctx context.Context, git *GitClient, target string) (string, error) {
	refs, err := git.RunContext(ctx, target, 2*time.Minute, true, true,
		"for-each-ref", "--format=%(refname) %(objectname)")
	return refs.Stdout, err
}

func recycleWorkspaceStatus(ctx context.Context, git *GitClient, target string) (string, error) {
	admin, err := git.RunContext(ctx, target, 2*time.Minute, true, true, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	adminPath := strings.TrimSpace(admin.Stdout)
	if !filepath.IsAbs(adminPath) {
		return "", fmt.Errorf("无法确认工作区 Git 登记路径：%s", target)
	}
	if err := requireModuleGitIdle(adminPath); err != nil {
		return "", fmt.Errorf("无法核对工作区 %s：%w", target, err)
	}
	status, err := git.RunContext(ctx, target, 2*time.Minute, true, true,
		"status", "--porcelain=v2", "--branch", "-z", "--no-renames", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	var changedPaths []string
	for _, line := range strings.Split(status.Stdout, "\x00") {
		switch {
		case strings.HasPrefix(line, "? "):
			changedPaths = append(changedPaths, strings.TrimPrefix(line, "? "))
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "u "):
			fields := 8
			if line[0] == 'u' {
				fields = 10
			}
			file, ok := parsePorcelainFile(line, line[0], fields)
			if !ok {
				return "", errors.New("无法核对工作区中的改动文件")
			}
			changedPaths = append(changedPaths, file.Path)
		}
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte(status.Stdout))
	if err := fingerprintModuleChangedPaths(ctx, target, changedPaths, digest); err != nil {
		return "", err
	}
	if err := requireModuleGitIdle(adminPath); err != nil {
		return "", fmt.Errorf("无法核对工作区 %s：%w", target, err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (l *WorkspaceLifecycle) checkTaskRecycleGuard(ctx context.Context, taskPath string, manifest domain.TaskManifest, expected string) error {
	current, err := l.taskRecycleGuard(ctx, taskPath, manifest)
	if err != nil {
		return err
	}
	if current != expected {
		return errors.New("工作区身份或 Git 状态已变化，请重新检查")
	}
	return nil
}

func (l *WorkspaceLifecycle) recycleTaskWithRecovery(ctx context.Context, config domain.AppConfig, taskPath string, manifest domain.TaskManifest, proofs []forcedWorkspaceProof, before, revalidate func() error) ([]string, error) {
	records := make([]*workspaceRecycleRecord, 0, len(proofs))
	abort := func(cause error) error {
		for _, record := range records {
			if err := record.abort(); err != nil {
				cause = errors.Join(cause, err)
			}
		}
		return cause
	}
	for _, proof := range proofs {
		if proof.workspace.Strategy != domain.WorkspaceStandardWorktree {
			continue
		}
		record, err := l.prepareTaskWorkspaceRecycle(ctx, taskPath, manifest, proof)
		if err != nil {
			return nil, abort(err)
		}
		records = append(records, record)
	}
	err := l.recycleTaskDirectory(ctx, config, taskPath, manifest, before, func() error {
		current, err := l.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(current, manifest) {
			return errors.New("任务清单已变化，请重新检查")
		}
		if err := revalidate(); err != nil {
			return err
		}
		for _, record := range records {
			if err := record.revalidate(ctx, l.Git); err != nil {
				return err
			}
		}
		return nil
	})
	var warnings []string
	for _, record := range records {
		if _, statErr := os.Lstat(record.Target); os.IsNotExist(statErr) {
			if finishErr := record.finish(l.Git); finishErr != nil {
				warnings = append(warnings, finishErr.Error())
			}
		} else if err == nil {
			warnings = append(warnings, fmt.Sprintf("未确认工作区已回收：%s", record.Target))
		}
	}
	if err != nil {
		return warnings, abort(err)
	}
	return warnings, nil
}
