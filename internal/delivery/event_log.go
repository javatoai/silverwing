package delivery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
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
