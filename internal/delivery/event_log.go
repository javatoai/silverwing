package delivery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"silverwing-go/internal/domain"
)

type applicationEvent struct {
	Timestamp string            `json:"timestamp"`
	Level     string            `json:"level"`
	Event     string            `json:"event"`
	Message   string            `json:"message"`
	Metadata  map[string]string `json:"metadata"`
}

// recordApplicationEvent mirrors the source event sink: logging is best-effort
// and must never change the outcome of a Git delivery operation.
func (s *Service) recordApplicationEvent(level, name, message string, metadata map[string]string) {
	logs := s.paths.Logs
	if logs == "" {
		logs = filepath.Join(s.paths.Home, "logs")
	}
	now := time.Now()
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		zone = time.FixedZone("CST", 8*60*60)
	}
	local := now.In(zone)
	entry := applicationEvent{
		Timestamp: formatSilverwingTime(now), Level: level, Event: name, Message: message,
		Metadata: metadata,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	path := filepath.Join(logs, "application-"+local.Format("2006-01-02")+".jsonl")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = withWaitingLock(ctx, path+".lock", func() error {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		if _, err = file.Write(append(data, '\n')); err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		return err
	})
}

func (s *Service) recordTagHistory(ctx context.Context, taskDirectory string, operation domain.TagOperation) error {
	if err := s.store.appendHistory(ctx, taskDirectory, operation); err != nil {
		return err
	}
	metadata := map[string]string{
		"operationId": operation.OperationID,
		"folderName":  operation.FolderName,
		"service":     operation.ServiceName,
		"state":       string(operation.State),
		"tag":         value(operation.Tag),
	}
	if operation.State == domain.TagOperationSuccess {
		s.recordApplicationEvent("INFO", "tag.build.completed", "Tag构建成功", metadata)
	} else {
		message := value(operation.Message)
		if message == "" {
			message = "Tag构建未成功"
		}
		s.recordApplicationEvent("ERROR", "tag.build.completed", message, metadata)
	}
	return nil
}

func (s *Service) recordTagBuildStarted(operation domain.TagOperation) {
	s.recordApplicationEvent("INFO", "tag.build.started", "开始Tag构建", map[string]string{
		"operationId": operation.OperationID,
		"folderName":  operation.FolderName,
		"service":     operation.ServiceName,
		"batchId":     value(operation.BatchID),
	})
}
