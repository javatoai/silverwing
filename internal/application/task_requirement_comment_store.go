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

	"silverwing-go/internal/domain"
	"silverwing-go/internal/integrations"
	"silverwing-go/internal/storage"
)

type taskRequirementCommentRecord struct {
	TaskRequirementCommentSyncStatus
	TaskPath        string                                      `json:"taskPath"`
	TaskCreatedAt   string                                      `json:"taskCreatedAt"`
	ExpectedAccount string                                      `json:"expectedAccount,omitempty"`
	Scope           integrations.TaskRequirementCommentScope    `json:"scope"`
	ConfirmedBody   string                                      `json:"confirmedBody,omitempty"`
	Attempt         *integrations.TaskRequirementCommentAttempt `json:"attempt,omitempty"`
	Generation      uint64                                      `json:"generation"`
	BlockedBy       string                                      `json:"blockedBy,omitempty"`
}

type taskRequirementCommentStore struct {
	path    string
	records map[string]taskRequirementCommentRecord
}

type taskRequirementCommentFile struct {
	Version int                                     `json:"version"`
	Records map[string]taskRequirementCommentRecord `json:"records"`
}

func taskRequirementCommentKey(path, createdAt string) string {
	identity := sha256.Sum256([]byte(filepath.Base(path) + "\x00" + createdAt))
	return hex.EncodeToString(identity[:])
}

func loadTaskRequirementCommentStore(home string) (taskRequirementCommentStore, error) {
	s := taskRequirementCommentStore{path: filepath.Join(home, "task-requirement-comments.json"), records: make(map[string]taskRequirementCommentRecord)}
	if !filepath.IsAbs(home) {
		return s, errors.New("无法定位任务评论同步的应用数据目录")
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("读取任务评论同步记录失败：%w", err)
	}
	var saved taskRequirementCommentFile
	if err := json.Unmarshal(data, &saved); err != nil || saved.Version != 1 || saved.Records == nil {
		return s, errors.New("任务评论同步记录格式无效")
	}
	commentOwners := make(map[[3]string]bool)
	for key, record := range saved.Records {
		if !filepath.IsAbs(record.TaskPath) || record.TaskCreatedAt == "" || domain.ParseFeishuWorkItemLink(record.RequirementURL) == nil ||
			key != taskRequirementCommentKey(record.TaskPath, record.TaskCreatedAt) || record.Generation == 0 {
			return s, errors.New("任务评论同步记录归属无效")
		}
		if record.CommentID != "" && record.ConfirmedBody == "" ||
			(record.CommentID != "" || record.Attempt != nil) && (record.Scope.ProjectKey == "" || record.Scope.WorkItemID == "" || record.Scope.Account == "") ||
			record.Attempt != nil && (record.Attempt.Body == "" || record.Attempt.CommentID != record.CommentID) {
			return s, errors.New("任务评论同步记录缺少发布依据")
		}
		if record.Attempt != nil {
			if _, err := time.Parse(time.RFC3339Nano, record.Attempt.StartedAt); err != nil {
				return s, errors.New("任务评论同步记录缺少有效的发送时间")
			}
		}
		if record.CommentID != "" {
			comment := [3]string{record.Scope.ProjectKey, record.Scope.WorkItemID, record.CommentID}
			if commentOwners[comment] {
				return s, errors.New("同一条同步评论关联了多个任务")
			}
			commentOwners[comment] = true
		}
	}
	s.records = saved.Records
	return s, nil
}

// The worker and all callers hold its state lock while updating the journal.
func (s *taskRequirementCommentStore) save(key string, record taskRequirementCommentRecord) error {
	previous, existed := s.records[key]
	s.records[key] = record
	data, err := json.Marshal(taskRequirementCommentFile{Version: 1, Records: s.records})
	if err == nil {
		err = storage.WriteFileAtomic(s.path, data, 0600)
	}
	if err == nil {
		return nil
	}
	if existed {
		s.records[key] = previous
	} else {
		delete(s.records, key)
	}
	return fmt.Errorf("保存任务评论同步记录失败：%w", err)
}
