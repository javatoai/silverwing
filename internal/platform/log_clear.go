package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"silverwing-go/internal/storage"
)

type ApplicationLogClearResult struct {
	ClearedFiles int    `json:"clearedFiles"`
	FailedFiles  int    `json:"failedFiles"`
	Changed      bool   `json:"changed"`
	Error        string `json:"error,omitempty"`
}

func (s *Service) clearApplicationLogs(ctx context.Context, payload map[string]any) (ApplicationLogClearResult, error) {
	result := ApplicationLogClearResult{}
	if err := requireConfirmed(payload, "清空应用日志"); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	home, err := filepath.Abs(s.paths.Home)
	if err != nil || s.paths.Home == "" {
		return result, errors.New("当前应用目录无效")
	}
	expected, err := filepath.Abs(payloadString(payload, "expectedHome"))
	if err != nil || payloadString(payload, "expectedHome") == "" || !samePath(home, expected) {
		return result, errors.New("应用目录已变化，请重新确认")
	}
	if payloadString(payload, "path") != "" || payloadString(payload, "name") != "" {
		return result, errors.New("清空应用日志不接受外部路径")
	}
	logs, err := filepath.Abs(s.paths.Logs)
	if err != nil || !samePath(logs, filepath.Join(home, "logs")) {
		return result, errors.New("日志目录不属于当前应用目录")
	}
	home, err = filepath.EvalSymlinks(home)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, errors.New("无法读取应用目录")
	}
	parent, err := os.OpenRoot(home)
	if err != nil {
		return result, errors.New("无法读取应用目录")
	}
	defer parent.Close()
	directory, err := parent.Lstat("logs")
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil || !directory.IsDir() || applicationLogReparse(directory) {
		return result, errors.New("日志目录不可用或包含链接")
	}
	root, err := parent.OpenRoot("logs")
	if err != nil {
		return result, errors.New("无法打开日志目录")
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(directory, opened) {
		return result, errors.New("日志目录已变化，请重试")
	}
	dir, err := root.Open(".")
	if err != nil {
		return result, errors.New("无法读取日志目录")
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return result, errors.New("无法读取日志目录")
	}
	names := []string{}
	for _, entry := range entries {
		if eventLogName.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	files := []*os.File{}
	releases := []func(){}
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}()
	// Prepare every writable handle before changing content. The same daily-file
	// locks are used by delivery.recordApplicationEvent; no logger is stopped.
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		info, err := root.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || applicationLogReparse(info) {
			return result, errors.New("应用日志包含非普通文件，未执行清空")
		}
		lockName := name + ".lock"
		if lockInfo, err := root.Lstat(lockName); err == nil {
			if !lockInfo.Mode().IsRegular() || applicationLogReparse(lockInfo) {
				return result, errors.New("日志锁文件不可用，未执行清空")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return result, errors.New("无法检查日志锁文件")
		}
		release, err := storage.AcquireLock(filepath.Join(root.Name(), lockName))
		if err != nil {
			return result, errors.New(applicationLogClearReason(err))
		}
		releases = append(releases, release)
		file, err := root.OpenFile(name, os.O_WRONLY, 0)
		if err != nil {
			return result, errors.New(applicationLogClearReason(err))
		}
		files = append(files, file)
		actual, err := file.Stat()
		if err != nil || !actual.Mode().IsRegular() || applicationLogReparse(actual) || !os.SameFile(info, actual) {
			return result, errors.New("日志文件已变化，未执行清空")
		}
	}
	return truncatePreparedApplicationLogs(ctx, files), nil
}

func truncatePreparedApplicationLogs(ctx context.Context, files []*os.File) ApplicationLogClearResult {
	result := ApplicationLogClearResult{}
	for index, file := range files {
		err := ctx.Err()
		if err == nil {
			err = file.Truncate(0)
			if err == nil {
				result.Changed = true
				err = file.Sync()
			}
		}
		if err != nil {
			result.FailedFiles = len(files) - index
			result.Error = applicationLogClearReason(err)
			return result
		}
		result.ClearedFiles++
	}
	return result
}

func applicationLogClearReason(err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "清空已取消"
	case errors.Is(err, storage.ErrLockBusy):
		return "日志正在写入，请稍后重试"
	case errors.Is(err, os.ErrPermission):
		return "没有清空权限或日志正被占用"
	default:
		return "日志清空未完成，请刷新后重试"
	}
}
