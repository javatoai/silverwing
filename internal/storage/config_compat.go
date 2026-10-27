package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"silverwing-go/internal/domain"
)

// Compatibility belongs only to settings. Project recognized fields before
// invoking the domain decoder, so its type, enum, null and required-key checks
// (including nested UnmarshalJSON methods) still run unchanged.
func knownConfigJSON(data []byte, t reflect.Type) ([]byte, error) {
	if configJSONNull(data) {
		return data, nil
	}
	t = configElementType(t)
	switch t.Kind() {
	case reflect.Struct:
		var source map[string]json.RawMessage
		if err := json.Unmarshal(data, &source); err != nil {
			return nil, err
		}
		fields := configJSONFields(t)
		out := map[string]json.RawMessage{}
		for name, raw := range source {
			field, known := fields[name]
			if !known {
				continue
			}
			value, err := knownConfigJSON(raw, field)
			if err != nil {
				return nil, fmt.Errorf("field %s: %w", name, err)
			}
			out[name] = value
		}
		// These storage-owned shapes have no domain defaulting unmarshaler.
		defaults := map[string]json.RawMessage{}
		switch t {
		case reflect.TypeOf(domain.MaterialsGitSyncSettings{}):
			raw, _ := json.Marshal(domain.DefaultMaterialsGitSyncSettings())
			_ = json.Unmarshal(raw, &defaults)
		}
		for name, raw := range defaults {
			if _, present := out[name]; !present {
				out[name] = raw
			}
		}
		return json.Marshal(out)
	case reflect.Slice, reflect.Array:
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, err
		}
		for i, raw := range items {
			value, err := knownConfigJSON(raw, t.Elem())
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
			items[i] = value
		}
		return json.Marshal(items)
	case reflect.Map:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return nil, err
		}
		for name, raw := range fields {
			value, err := knownConfigJSON(raw, t.Elem())
			if err != nil {
				return nil, err
			}
			fields[name] = value
		}
		return json.Marshal(fields)
	default:
		return data, nil
	}
}

// Unknown values stay as RawMessage throughout a read-modify-write transaction;
// in particular, numbers never pass through float64. Only settings still present
// in the new typed value inherit extensions from the latest locked disk snapshot.
func mergeConfigJSON(previous, next []byte, t reflect.Type, path string) ([]byte, error) {
	if len(previous) == 0 || configJSONNull(previous) || configJSONNull(next) {
		return next, nil
	}
	t = configElementType(t)
	switch t.Kind() {
	case reflect.Struct, reflect.Map:
		var before, after map[string]json.RawMessage
		if err := json.Unmarshal(previous, &before); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(next, &after); err != nil {
			return nil, err
		}
		fields := configJSONFields(t)
		for name, raw := range after {
			field, known := fields[name]
			if t.Kind() == reflect.Map {
				field, known = t.Elem(), true
			}
			if !known {
				continue
			}
			merged, err := mergeConfigJSON(before[name], raw, field, path+"."+name)
			if err != nil {
				return nil, err
			}
			after[name] = merged
		}
		if t.Kind() == reflect.Struct {
			for name, raw := range before {
				if _, known := fields[name]; !known && !retiredConfigField(t, name) {
					after[name] = raw
				}
			}
		}
		return json.MarshalIndent(after, "", "  ")
	case reflect.Slice, reflect.Array:
		var before, after []json.RawMessage
		if err := json.Unmarshal(previous, &before); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(next, &after); err != nil {
			return nil, err
		}
		if len(after) == 0 {
			return next, nil // Explicit deletion also deletes row extensions.
		}
		hasExtensions := false
		for _, item := range before {
			extra, err := unknownConfigJSON(item, t.Elem())
			if err != nil {
				return nil, err
			}
			hasExtensions = hasExtensions || len(extra) != 0
		}
		if !hasExtensions {
			return next, nil
		}
		matches, err := matchConfigRows(before, after, t.Elem(), path)
		if err != nil {
			return nil, err
		}
		for i, oldIndex := range matches {
			if oldIndex < 0 {
				continue
			}
			merged, err := mergeConfigJSON(before[oldIndex], after[i], t.Elem(), fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			after[i] = merged
		}
		return json.MarshalIndent(after, "", "  ")
	default:
		return next, nil
	}
}

func matchConfigRows(before, after []json.RawMessage, t reflect.Type, path string) ([]int, error) {
	idField := configRowIdentity(configElementType(t))
	key := func(raw []byte) (string, error) {
		if idField != "" {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				return "", err
			}
			var id string
			if err := json.Unmarshal(fields[idField], &id); err != nil || id == "" {
				return "", fmt.Errorf("%s: 列表缺少有效标识 %s", path, idField)
			}
			return id, nil
		}
		// Legacy bootstrap rows have no ID. Only an exact, unique recognized
		// value (with its defaults applied) is safe to match across reorders.
		known, err := knownConfigJSON(raw, t)
		if err != nil {
			return "", err
		}
		value := reflect.New(t)
		if err := domain.DecodeStrict(known, value.Interface()); err != nil {
			return "", err
		}
		canonical, err := json.Marshal(value.Interface())
		return string(canonical), err
	}
	oldRows, newRows := map[string][]int{}, map[string][]int{}
	for _, side := range []struct {
		items []json.RawMessage
		rows  map[string][]int
	}{{before, oldRows}, {after, newRows}} {
		for i, raw := range side.items {
			id, err := key(raw)
			if err != nil {
				return nil, err
			}
			side.rows[id] = append(side.rows[id], i)
		}
	}
	matches := make([]int, len(after))
	for i := range matches {
		matches[i] = -1
	}
	unmatchedExtensions, unmatchedNew := false, false
	ambiguous := func() error {
		return fmt.Errorf("%s: 无法唯一匹配列表项目，未保存配置，以免丢失或错配其他版本的字段", path)
	}
	for id, old := range oldRows {
		new := newRows[id]
		if len(old) == 1 && len(new) == 1 {
			matches[new[0]] = old[0]
			continue
		}
		for _, i := range old {
			extra, err := unknownConfigJSON(before[i], t)
			if err != nil {
				return nil, err
			}
			if len(extra) != 0 {
				if len(new) > 0 {
					return nil, ambiguous()
				}
				unmatchedExtensions = true
			}
		}
	}
	for id := range newRows {
		unmatchedNew = unmatchedNew || len(oldRows[id]) == 0
	}
	if idField == "" && unmatchedExtensions && unmatchedNew {
		return nil, ambiguous()
	}
	return matches, nil
}

func configRowIdentity(t reflect.Type) string {
	switch t {
	case reflect.TypeOf(groupLayoutShard{}), reflect.TypeOf(domain.SkillSource{}), reflect.TypeOf(domain.CodexPluginMarketplaceSource{}):
		return "id"
	case reflect.TypeOf(domain.MeegleProjectConfig{}):
		return "project_key"
	}
	return ""
}

// Extract only extensions for archive comparison. Recognized settings already
// have business-aware comparison (including the proxy-target set). Empty nodes
// are omitted; array placeholders retain which row owns an extension.
func unknownConfigJSON(data []byte, t reflect.Type) ([]byte, error) {
	if len(data) == 0 || configJSONNull(data) {
		return nil, nil
	}
	t = configElementType(t)
	switch t.Kind() {
	case reflect.Struct, reflect.Map:
		var source map[string]json.RawMessage
		if err := json.Unmarshal(data, &source); err != nil {
			return nil, err
		}
		fields, out := configJSONFields(t), map[string]json.RawMessage{}
		for name, raw := range source {
			field, known := fields[name]
			if t.Kind() == reflect.Map {
				field, known = t.Elem(), true
			}
			if !known {
				if !retiredConfigField(t, name) {
					out[name] = raw
				}
				continue
			}
			extra, err := unknownConfigJSON(raw, field)
			if err != nil {
				return nil, err
			}
			if len(extra) != 0 {
				out[name] = extra
			}
		}
		if len(out) > 0 {
			return json.Marshal(out)
		}
	case reflect.Slice, reflect.Array:
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, err
		}
		anyExtensions := false
		for i, raw := range items {
			extra, err := unknownConfigJSON(raw, t.Elem())
			if err != nil {
				return nil, err
			}
			items[i] = extra
			anyExtensions = anyExtensions || len(extra) != 0
		}
		if anyExtensions {
			return json.Marshal(items)
		}
	}
	return nil, nil
}

func changedImportedShards(current, imported decodedConfig) ([]string, error) {
	changed := map[string]bool{}
	for _, name := range changedShardNames(current.Shards, imported.Shards) {
		changed[name] = true
	}
	names := []string{}
	for _, name := range shardNames {
		value, err := configShardValue(name, imported.Shards)
		if err != nil {
			return nil, err
		}
		before, err := unknownConfigJSON(current.Files[name], reflect.TypeOf(value))
		if err != nil {
			return nil, err
		}
		after, err := unknownConfigJSON(imported.Files[name], reflect.TypeOf(value))
		if err != nil {
			return nil, err
		}
		if changed[name] || !equalConfigJSON(before, after) {
			names = append(names, name)
		}
	}
	return names, nil
}

func equalConfigJSON(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	decode := func(data []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err
	}
	left, err := decode(a)
	if err != nil {
		return false
	}
	right, err := decode(b)
	return err == nil && reflect.DeepEqual(left, right)
}

func configJSONFields(t reflect.Type) map[string]reflect.Type {
	fields := map[string]reflect.Type{}
	if t.Kind() != reflect.Struct {
		return fields
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if field.PkgPath != "" || name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}

func configElementType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func configJSONNull(data []byte) bool { return bytes.Equal(bytes.TrimSpace(data), []byte("null")) }

func retiredConfigField(t reflect.Type, name string) bool {
	return t == reflect.TypeOf(integrationsShard{}) && (name == "larkExecutablePath" || name == "requirementMaterialsFeishuTarget")
}

// An archive replaces settings, rather than inheriting extensions from the
// target installation. Keep its raw source values, except retired cloud config.
func importedConfigFiles(imported decodedConfig) (map[string][]byte, error) {
	files := make(map[string][]byte, len(imported.Files))
	for name, data := range imported.Files {
		files[name] = data
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(files["integrations.json"], &fields); err != nil {
		return nil, err
	}
	for name := range fields {
		if retiredConfigField(reflect.TypeOf(integrationsShard{}), name) {
			delete(fields, name)
		}
	}
	if _, present := fields["commandProxyTargets"]; present {
		raw, err := json.Marshal(imported.Shards.Integrations.CommandProxyTargets)
		if err != nil {
			return nil, err
		}
		fields["commandProxyTargets"] = raw
	}
	data, err := json.MarshalIndent(fields, "", "  ")
	files["integrations.json"] = data
	return files, err
}
