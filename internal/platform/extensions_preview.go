package platform

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"unicode/utf8"
)

func readExtensionText(path string, limit int64) ([]byte, error) {
	data, err := readBounded(path, limit)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("文件不是有效的 UTF-8 文本：%s", path)
	}
	return data, nil
}

func readPluginPreviewManifest(cacheRoot, directory string) (map[string]any, error) {
	if !within(cacheRoot, directory) || !isDir(directory) || isSymlink(directory) {
		return nil, fmt.Errorf("插件预览路径超出 Marketplace 缓存目录或目录无效")
	}
	path := filepath.Join(directory, ".codex-plugin", "plugin.json")
	if err := ensureNoSymlinksBetween(cacheRoot, path); err != nil {
		return nil, err
	}
	data, err := readExtensionText(path, maxPluginBytes)
	if err != nil {
		return nil, fmt.Errorf("插件缺少 .codex-plugin/plugin.json：%w", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("插件清单不是 JSON 对象：%w", err)
	}
	if manifest == nil {
		return nil, fmt.Errorf("插件清单不是 JSON 对象")
	}
	return manifest, nil
}
