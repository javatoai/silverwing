package genbuapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// ConfigSnapshotRequest identifies an application's latest ConfigMap. An empty
// File lists names; otherwise File selects one exact file name.
type ConfigSnapshotRequest struct {
	Service     string
	Environment string
	File        string
}

// ConfigSnapshotResult identifies a ConfigMap snapshot and contains either a
// list of file names or the unredacted text of one selected file.
type ConfigSnapshotResult struct {
	Service     string    `json:"service"`
	Environment string    `json:"environment"`
	Namespace   string    `json:"namespace"`
	Name        string    `json:"name"`
	UpdateTime  string    `json:"update_time"`
	SnapshotID  string    `json:"snapshot_id"`
	Files       *[]string `json:"files,omitempty"`
	File        string    `json:"file,omitempty"`
	Content     *string   `json:"content,omitempty"`
}

// QueryConfigSnapshot reads the same configuration that ConfigMap edits target.
func (client *Client) QueryConfigSnapshot(ctx context.Context, request ConfigSnapshotRequest) (ConfigSnapshotResult, error) {
	environment, err := normalizeEnvironmentName(request.Environment)
	if err != nil {
		return ConfigSnapshotResult{}, err
	}
	service := strings.TrimSpace(request.Service)
	if service == "" || (request.File != "" && strings.TrimSpace(request.File) == "") {
		return ConfigSnapshotResult{}, fmt.Errorf("必须指定服务和有效的目标文件名")
	}

	var result ConfigSnapshotResult
	err = client.requestToken(ctx, func(worker *genbuClient, token string) error {
		target, err := worker.resolveLatestConfigTarget(service, environment, token)
		if err != nil {
			return err
		}
		result, err = target.queryFile(request.File)
		return err
	})
	return result, err
}

type configSnapshotTarget struct {
	service     resolvedService
	environment string
	namespace   string
	updateTime  string
	row         map[string]any
}

func (client *genbuClient) resolveLatestConfigTarget(service, environment, token string) (configSnapshotTarget, error) {
	resolved, err := client.resolveServiceContext(service, token)
	if err != nil {
		return configSnapshotTarget{}, configEditReadError(err, "无法解析 Genbu 服务")
	}
	namespace, err := client.resolveConfigNamespace(resolved, "", environment, token)
	if err != nil {
		return configSnapshotTarget{}, err
	}
	row, updateTime, err := client.readConfigEditRow(environment, namespace, token, resolved.AppID)
	if err != nil {
		return configSnapshotTarget{}, err
	}
	return configSnapshotTarget{
		service: resolved, environment: environment, namespace: namespace, updateTime: updateTime, row: row,
	}, nil
}

func (target configSnapshotTarget) queryFile(name string) (ConfigSnapshotResult, error) {
	snapshotID, err := target.snapshotID()
	if err != nil {
		return ConfigSnapshotResult{}, err
	}
	result := ConfigSnapshotResult{
		Service: target.service.Name, Environment: target.environment,
		Namespace: target.namespace, Name: valueString(target.row, "name"),
		UpdateTime: target.updateTime, SnapshotID: snapshotID,
	}
	if name == "" {
		files, err := configFileNames(target.row)
		if err != nil {
			return ConfigSnapshotResult{}, err
		}
		result.Files = &files
		return result, nil
	}
	file, err := configEditFile(target.row, name)
	if err != nil {
		return ConfigSnapshotResult{}, err
	}
	content, ok := file["content"].(string)
	if !ok {
		return ConfigSnapshotResult{}, fmt.Errorf("目标文件缺少文本正文：%w", ErrConfigTarget)
	}
	result.File, result.Content = name, &content
	return result, nil
}

func (target configSnapshotTarget) snapshotID() (string, error) {
	// Bind even the fields and other file bodies that the query does not return.
	payload, err := json.Marshal(struct {
		Environment string
		Namespace   string
		AppID       string
		Row         map[string]any
	}{target.environment, target.namespace, target.service.AppID, target.row})
	if err != nil {
		return "", fmt.Errorf("无法生成配置快照 ID")
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func configFileNames(row map[string]any) ([]string, error) {
	files, err := configFileRecords(row)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		name, ok := file["name"].(string)
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("Genbu ConfigMap 缺少有效的配置文件名：%w", ErrConfigTarget)
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("ConfigMap 内存在多个同名文件，无法唯一定位：%w", ErrConfigTarget)
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names, nil
}
