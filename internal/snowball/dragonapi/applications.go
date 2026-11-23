package dragonapi

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Snapshot: Dragon ANDCN application list, verified on 2026-10-30.
//
//go:embed applications.json
var applicationSnapshot []byte

var registry, registryIndex, registryError = loadApplications(applicationSnapshot)

func loadApplications(data []byte) ([]Application, map[string]int, error) {
	var apps []Application
	if err := json.Unmarshal(data, &apps); err != nil {
		return nil, nil, fmt.Errorf("内建应用映射无效")
	}
	index := make(map[string]int)
	ids := make(map[string]bool)
	for i, app := range apps {
		if app.Alias == "" || app.AppID == "" || app.AppName == "" || app.AppCNName == "" || app.Version != applicationVersion || ids[app.AppID] {
			return nil, nil, fmt.Errorf("内建应用映射不完整或应用编号重复")
		}
		ids[app.AppID] = true
		keys := append([]string{app.Alias, app.AppName}, app.Aliases...)
		for _, key := range keys {
			key = strings.ToLower(strings.TrimSpace(key))
			if key == "" {
				return nil, nil, fmt.Errorf("内建应用简称不能为空")
			}
			if other, ok := index[key]; ok && other != i {
				return nil, nil, fmt.Errorf("内建应用简称冲突：%s", key)
			}
			index[key] = i
		}
	}
	return apps, index, nil
}

// ResolveApplications rejects unknown inputs and duplicate applications locally.
func ResolveApplications(aliases []string) ([]Application, error) {
	if registryError != nil {
		return nil, registryError
	}
	if len(aliases) == 0 {
		return nil, fmt.Errorf("请提供应用简称集合，例如 api,app,opc")
	}
	result := make([]Application, 0, len(aliases))
	seen := make(map[string]bool)
	for _, alias := range aliases {
		key := strings.ToLower(strings.TrimSpace(alias))
		i, ok := registryIndex[key]
		if !ok {
			return nil, fmt.Errorf("未知应用简称 %q；应用映射固定在程序内，请核对简称", alias)
		}
		app := registry[i]
		if seen[app.AppID] {
			return nil, fmt.Errorf("应用 %s 重复指定", app.Alias)
		}
		seen[app.AppID] = true
		app.Aliases = append([]string(nil), app.Aliases...)
		result = append(result, app)
	}
	return result, nil
}

func appAlias(id, name string) string {
	for _, app := range registry {
		if app.AppID == id {
			return app.Alias
		}
	}
	if i, ok := registryIndex[strings.ToLower(name)]; ok {
		return registry[i].Alias
	}
	return "未知应用"
}

func NormalizeCreateInput(input CreateInput) (CreateInput, error) {
	input.Meego = strings.TrimSpace(input.Meego)
	if input.Meego == "" {
		return CreateInput{}, fmt.Errorf("Meego 编号不能为空")
	}
	apps, err := ResolveApplications(input.Apps)
	if err != nil {
		return CreateInput{}, err
	}
	input.Apps = make([]string, 0, len(apps))
	for _, app := range apps {
		input.Apps = append(input.Apps, app.Alias)
	}
	return input, nil
}
