package application

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"silverwing-go/internal/documents"
)

func (a *App) platformAction(ctx context.Context, action Action) (ActionResult, error) {
	payload := map[string]any{}
	for key, value := range action.Payload {
		payload[key] = value
	}
	if path := actionPath(action); path != "" {
		payload["path"] = path
	}
	if action.Text != "" {
		payload["text"] = action.Text
	}
	if action.TaskID != "" {
		_, taskPath, err := a.task(action)
		if err != nil {
			return ActionResult{}, err
		}
		payload["taskPath"] = taskPath
	}
	kind := action.Kind
	if kind == "skills.files/copy" {
		data, err := a.Platform.Execute(ctx, "skills.files/copy-path", payload)
		if err != nil {
			return ActionResult{}, err
		}
		object, ok := data.(map[string]any)
		if !ok {
			return ActionResult{}, errors.New("Skill 文件路径响应无效")
		}
		path, _ := object["path"].(string)
		if strings.TrimSpace(path) == "" {
			return ActionResult{}, errors.New("Skill 文件路径响应为空")
		}
		if err := documents.NewClipboardService().CopyFiles(ctx, []string{path}); err != nil {
			return ActionResult{}, err
		}
		return ActionResult{Message: "已复制 Skill 文件", Path: path}, nil
	}
	if kind == "skills.refresh" {
		kind = "skills.catalog"
	}
	data, err := a.Platform.Execute(ctx, kind, payload)
	if err != nil {
		return ActionResult{}, err
	}
	result := ActionResult{Message: "操作结果已读取", Data: data}
	if kind == "skills.catalog" {
		result.Message = "本机 Skills 已读取"
	}
	if kind == "diagnostics.export" {
		if object, ok := data.(map[string]any); ok {
			if path, ok := object["path"].(string); ok {
				result.Path = path
			}
		}
		result.Message = "诊断包已导出"
	}
	if strings.HasSuffix(kind, "/save") || strings.HasSuffix(kind, "/install") || kind == "skills.install" {
		result.Message = "操作已返回，请查看安装或配置结果"
	}
	return result, nil
}

func localSkillRows(data any) ([]Skill, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var catalog struct {
		Root   string `json:"root"`
		Skills []struct {
			DirectoryName string `json:"directoryName"`
			Name          string `json:"name"`
			Description   string `json:"description"`
		} `json:"skills"`
	}
	if err = json.Unmarshal(encoded, &catalog); err != nil {
		return nil, err
	}
	items := make([]Skill, 0, len(catalog.Skills))
	for _, entry := range catalog.Skills {
		items = append(items, Skill{Name: entry.Name, Description: entry.Description, Path: filepath.Join(catalog.Root, entry.DirectoryName), Source: "本机"})
	}
	return items, nil
}
