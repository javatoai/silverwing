package documents

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

type TemplateStore struct{ Paths storage.Paths }

func NewTemplateStore(paths storage.Paths) *TemplateStore { return &TemplateStore{Paths: paths} }

func (s *TemplateStore) List() ([]domain.AgentTaskTemplate, error) {
	data, err := os.ReadFile(s.Paths.AgentTaskTemplates)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(strings.TrimSpace(string(data))) == 0) {
		return []domain.AgentTaskTemplate{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := []domain.AgentTaskTemplate{}
	if err = json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	if items == nil {
		return nil, errors.New("任务说明模板必须是 JSON 数组")
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func (s *TemplateStore) Save(ctx context.Context, id, name, content string) ([]domain.AgentTaskTemplate, error) {
	return s.update(ctx, func(items []domain.AgentTaskTemplate) ([]domain.AgentTaskTemplate, error) {
		name, content = strings.TrimSpace(name), strings.TrimSpace(content)
		if name == "" {
			return nil, errors.New("模板名称不能为空")
		}
		if content == "" {
			return nil, errors.New("模板内容不能为空")
		}
		found := false
		for i := range items {
			if id != "" && items[i].ID == id {
				items[i].Name = name
				items[i].Content = content
				items[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
				found = true
			}
		}
		if found {
			return items, nil
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, err
		}
		random[6] = (random[6] & 0x0f) | 0x40
		random[8] = (random[8] & 0x3f) | 0x80
		h := hex.EncodeToString(random[:])
		id = h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
		return append(items, domain.AgentTaskTemplate{ID: id, Name: name, Content: content, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}), nil
	})
}

func (s *TemplateStore) Remove(ctx context.Context, id string) ([]domain.AgentTaskTemplate, error) {
	return s.update(ctx, func(items []domain.AgentTaskTemplate) ([]domain.AgentTaskTemplate, error) {
		out := []domain.AgentTaskTemplate{}
		for _, item := range items {
			if item.ID != id {
				out = append(out, item)
			}
		}
		return out, nil
	})
}

func (s *TemplateStore) update(ctx context.Context, change func([]domain.AgentTaskTemplate) ([]domain.AgentTaskTemplate, error)) ([]domain.AgentTaskTemplate, error) {
	release, err := AcquireWaitingLock(ctx, filepath.Join(s.Paths.Locks, "agent-task-templates.lock"))
	if err != nil {
		return nil, err
	}
	defer release()
	items, err := s.List()
	if err != nil {
		return nil, err
	}
	items, err = change(items)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if names[name] {
			return nil, errors.New("模板名称不能重复")
		}
		names[name] = true
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = atomicWrite(s.Paths.AgentTaskTemplates, data, 0o644); err != nil {
		return nil, err
	}
	return s.List()
}

func AcquireWaitingLock(ctx context.Context, path string) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		release, err := storage.AcquireLock(path)
		if err == nil {
			return release, nil
		}
		if !errors.Is(err, storage.ErrLockBusy) {
			return nil, err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
