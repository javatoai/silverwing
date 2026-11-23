package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"silverwing-go/internal/storage"
)

type dragonQARecord struct {
	DragonQAState
	Account       string
	RequirementID string
	Remark        string
	AttemptedAt   time.Time
	SyncComment   bool
	CommentScope  string
}

func (r dragonQARecord) view(form DragonQAState) DragonQAState {
	switch r.Phase {
	case "":
		return form
	case DragonQAFailed:
		form.Phase = r.Phase
		form.Message = r.Message
		return form
	default:
		return r.DragonQAState
	}
}

type dragonQAStore struct {
	path   string
	taskID string
}

func (a *App) dragonQAStore(task DragonQATask) dragonQAStore {
	// The manifest creation identity survives moving the configured task root.
	digest := sha256.Sum256([]byte(task.TaskID + "\x00" + task.TaskCreatedAt))
	return dragonQAStore{path: filepath.Join(a.Paths.Home, "dragon-qa", hex.EncodeToString(digest[:])+".json"), taskID: task.TaskID}
}

func (s dragonQAStore) lock() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return nil, err
	}
	return storage.AcquireLock(s.path + ".lock")
}

func (s dragonQAStore) load() (dragonQARecord, error) {
	var record dragonQARecord
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return record, nil
	}
	if err != nil {
		return record, fmt.Errorf("读取提测记录失败：%w", err)
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return record, fmt.Errorf("提测记录格式无效：%w", err)
	}
	if record.TaskID != s.taskID || record.RequirementID == "" || record.Account == "" || record.AttemptedAt.IsZero() {
		return record, errors.New("无法确认提测记录归属")
	}
	switch record.Phase {
	case DragonQASubmitting, DragonQAUnknown, DragonQAFailed:
	case DragonQAApproved:
		if record.ApprovalID == "" || record.ApprovalURL == "" {
			return record, errors.New("已保存的提测记录缺少审批编号或链接")
		}
	default:
		return record, errors.New("提测记录状态无效")
	}
	return record, nil
}

func (s dragonQAStore) save(record dragonQARecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := storage.WriteFileAtomic(s.path, data, 0600); err != nil {
		return fmt.Errorf("保存提测记录失败：%w", err)
	}
	return nil
}
