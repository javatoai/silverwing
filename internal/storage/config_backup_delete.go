package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type listedConfigBackup struct {
	backup    Backup
	info      os.FileInfo
	directory os.FileInfo
}

var errConfigBackupDeleteUnsupported = errors.New("当前平台尚不支持按文件身份安全删除备份")

type BackupDeleteFailure struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type BackupDeleteResult struct {
	DeletedIDs []string              `json:"deletedIds"`
	Failures   []BackupDeleteFailure `json:"failures"`
	Backups    []Backup              `json:"backups"`
}

// Resolve the selected home once, then anchor all leaf operations to its real
// backups directory. A redirected backups directory is never a deletion root.
func (s *ConfigStore) openConfigBackups() (*os.Root, os.FileInfo, error) {
	home, err := filepath.Abs(s.paths.Home)
	if err != nil {
		return nil, nil, err
	}
	configured, err := filepath.Abs(s.paths.Backups)
	if err != nil || configured != filepath.Join(home, "backups") {
		return nil, nil, errors.New("备份目录不属于当前配置目录")
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, nil, err
	}
	parent, err := os.OpenRoot(home)
	if err != nil {
		return nil, nil, err
	}
	defer parent.Close()
	info, err := parent.Lstat("backups")
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() || configBackupReparse(info) {
		return nil, nil, errors.New("备份目录不能是链接或重解析点")
	}
	root, err := parent.OpenRoot("backups")
	if err != nil {
		return nil, nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, nil, errors.New("备份目录已变化，请重新读取")
	}
	return root, opened, nil
}

func (s *ConfigStore) listConfigBackups(ctx context.Context) ([]Backup, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	root, directory, err := s.openConfigBackups()
	if errors.Is(err, os.ErrNotExist) {
		s.listedBackups = nil
		return []Backup{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	listed := make(map[string]listedConfigBackup)
	out := []Backup{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if !strings.HasPrefix(name, backupPrefix) || !strings.HasSuffix(name, ".zip") {
			continue
		}
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || configBackupReparse(info) {
			continue
		}
		backup := Backup{Path: filepath.Join(s.paths.Backups, name), ModifiedAt: info.ModTime()}
		for id, old := range s.listedBackups {
			if old.backup.Path == backup.Path && sameConfigBackup(old.info, info) && os.SameFile(old.directory, directory) {
				backup.ID = id
				break
			}
		}
		if backup.ID == "" {
			var token [16]byte
			if _, err := rand.Read(token[:]); err != nil {
				return nil, err
			}
			backup.ID = hex.EncodeToString(token[:])
		}
		listed[backup.ID] = listedConfigBackup{backup: backup, info: info, directory: directory}
		out = append(out, backup)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModifiedAt.After(out[j].ModifiedAt) })
	s.listedBackups = listed
	return out, ctx.Err()
}

func sameConfigBackup(expected, current os.FileInfo) bool {
	return expected != nil && current != nil && current.Mode().IsRegular() && !configBackupReparse(current) &&
		os.SameFile(expected, current) && expected.Size() == current.Size() && expected.ModTime().Equal(current.ModTime())
}

// DeleteBackupsContext accepts only opaque identities from this store's list.
// The config transaction gate also serializes retention/import/restore writes.
func (s *ConfigStore) DeleteBackupsContext(ctx context.Context, ids []string) (BackupDeleteResult, error) {
	result := BackupDeleteResult{DeletedIDs: []string{}, Failures: []BackupDeleteFailure{}, Backups: []Backup{}}
	if len(ids) == 0 || len(ids) > 100 {
		return result, errors.New("请选择 1 至 100 个已读取的备份")
	}
	for _, id := range ids {
		if len(id) != 32 {
			return result, errors.New("备份身份无效，请重新读取")
		}
		if _, err := hex.DecodeString(id); err != nil {
			return result, errors.New("备份身份无效，请重新读取")
		}
	}
	releaseProcess, err := s.acquireProcessLock(ctx)
	if err != nil {
		return result, err
	}
	defer releaseProcess()
	releaseFile, err := AcquireLock(filepath.Join(s.paths.Locks, configLockFile))
	if err != nil {
		return result, err
	}
	defer releaseFile()
	root, directory, err := s.openConfigBackups()
	if err != nil {
		return result, errors.New("无法读取当前备份目录，请重新读取备份")
	}
	defer root.Close()
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		s.backupMu.Lock()
		listed, ok := s.listedBackups[id]
		s.backupMu.Unlock()
		failure := BackupDeleteFailure{ID: id, Name: filepath.Base(listed.backup.Path)}
		switch {
		case ctx.Err() != nil:
			failure.Reason = "删除已取消"
		case !ok:
			failure.Name, failure.Reason = "", "备份身份已失效，请重新读取"
		case !os.SameFile(listed.directory, directory) || filepath.Dir(listed.backup.Path) != filepath.Clean(s.paths.Backups):
			failure.Reason = "备份目录已变化，请重新读取"
		default:
			failure.Reason = configBackupDeleteReason(removeListedConfigBackup(root, failure.Name, listed.info))
		}
		if failure.Reason != "" {
			result.Failures = append(result.Failures, failure)
		} else {
			result.DeletedIDs = append(result.DeletedIDs, id)
		}
	}
	// Preserve completed and failed outcomes even if cancellation or a later
	// listing error occurs; the UI only removes identities actually deleted.
	backups, listErr := s.listConfigBackups(context.WithoutCancel(ctx))
	if listErr != nil {
		return result, fmt.Errorf("删除结果已保留，重新读取备份失败")
	}
	result.Backups = backups
	return result, nil
}

func configBackupDeleteReason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errConfigBackupDeleteUnsupported):
		return err.Error()
	case errors.Is(err, os.ErrNotExist):
		return "文件已不存在，请重新读取"
	case errors.Is(err, os.ErrPermission):
		return "没有删除权限或文件正被占用"
	default:
		return "文件已变化、被占用或无法删除，请重新读取后重试"
	}
}
