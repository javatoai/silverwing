package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"silverwing-go/internal/production"
	"silverwing-go/internal/storage"
)

// GenbuConfigPendingError points to the query window for a possibly created approval.
type GenbuConfigPendingError struct {
	AttemptedAt time.Time `json:"attemptedAt"`
}

func (*GenbuConfigPendingError) Error() string {
	return "此配置改动的提交结果待核对，请查看 Genbu 配置审批"
}

func (*GenbuConfigPendingError) Unwrap() error { return production.ErrGenbuConfigSubmitUnknown }

func (a *App) genbuConfigSubmissionPath(changeID string) string {
	return filepath.Join(a.Paths.Home, "genbu-config-submissions", changeID+".json")
}

func checkGenbuConfigSubmission(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取配置审批提交记录失败：%w", err)
	}
	var pending GenbuConfigPendingError
	if err := json.Unmarshal(data, &pending); err != nil || pending.AttemptedAt.IsZero() {
		return errors.New("无法核对本地配置审批提交记录")
	}
	return &pending
}

func (a *App) recordGenbuConfigSubmission(path string) (*GenbuConfigPendingError, error) {
	unlock, err := storage.AcquireLock(filepath.Join(a.Paths.Locks, "genbu-config-submissions.lock"))
	if err != nil {
		return nil, fmt.Errorf("锁定配置审批提交记录失败：%w", err)
	}
	defer unlock()
	if err := checkGenbuConfigSubmission(path); err != nil {
		return nil, err
	}
	pending := &GenbuConfigPendingError{AttemptedAt: time.Now().UTC()}
	data, err := json.Marshal(pending)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o700)
	}
	if err == nil {
		err = storage.WriteFileAtomic(path, data, 0o600)
	}
	if err != nil {
		return nil, fmt.Errorf("保存配置审批提交记录失败：%w", err)
	}
	return pending, nil
}
