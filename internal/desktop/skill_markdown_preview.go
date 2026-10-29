package desktop

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

func bindSkillMarkdownPreviewImages(state *markdownEditorState, sourcePath, root string, files localPreviewFileResolver) {
	if state == nil || !state.readOnly || !state.detached || sourcePath == "" || root == "" {
		return
	}
	source, sourceErr := filepath.Abs(sourcePath)
	allowedRoot, rootErr := filepath.Abs(root)
	if sourceErr != nil || rootErr != nil || !markdownPathWithin(allowedRoot, source) {
		return
	}
	state.path, state.linkRoot, state.remoteImages = source, allowedRoot, true
	state.localPreviewFiles = files
}

func isSkillMarkdownPreview(state *markdownEditorState) bool {
	return state != nil && state.readOnly && state.detached && state.remoteImages && state.path != "" && state.linkRoot != "" &&
		(strings.HasPrefix(state.taskID, "local-skill:") || strings.HasPrefix(state.taskID, "extension-preview:"))
}

func (s *app) skillMarkdownPreviewCurrent(state *markdownEditorState) bool {
	if !isSkillMarkdownPreview(state) || state.closed {
		return false
	}
	if strings.HasPrefix(state.taskID, "local-skill:") {
		browser := s.settingsUnit.state.skillsBrowser
		return s.localSkillsViewActive() && browser != nil && browser.markdown == state &&
			filepath.Clean(state.path) == filepath.Join(browser.root, browser.selectedDirectory, filepath.FromSlash(browser.selectedPath))
	}
	preview := s.settingsUnit.state.settingsExtensionRequests
	return preview != nil && preview.previewMarkdown == state && s.settingsExtensionActionRelevant(preview.readActions["preview"])
}

func (s *app) resolveSkillMarkdownImage(state *markdownEditorState, destination string) (string, bool, error) {
	if !s.skillMarkdownPreviewCurrent(state) {
		return "", false, fmt.Errorf("Skill 预览已切换，无法读取此图片")
	}
	parsed, err := url.Parse(strings.ReplaceAll(destination, " ", "%20"))
	if err != nil {
		return "", false, fmt.Errorf("Skill 图片地址无效")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme == "http" || parsed.Scheme == "https" {
		if _, err := parseNativeRemoteImageURL(parsed.String()); err != nil {
			return "", false, err
		}
		return "", true, nil
	}
	if parsed.Scheme != "" || parsed.Host != "" || parsed.Path == "" || strings.HasPrefix(destination, "/") || strings.HasPrefix(destination, `\`) || strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, `\`) || filepath.IsAbs(filepath.FromSlash(parsed.Path)) {
		return "", false, fmt.Errorf("仅支持 Skill 目录内的相对图片路径或 HTTP(S) 图片")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(state.path), filepath.FromSlash(parsed.Path)))
	if !markdownPathWithin(state.linkRoot, path) {
		return "", false, fmt.Errorf("图片路径不能越出当前 Skill 目录")
	}
	relative, err := filepath.Rel(state.linkRoot, path)
	if err != nil {
		return "", false, fmt.Errorf("Skill 图片路径无效")
	}
	if state.localPreviewFiles == nil {
		return "", false, fmt.Errorf("Skill 图片读取服务不可用")
	}
	path, err = state.localPreviewFiles.ResolveLocalFile(context.Background(), state.linkRoot, relative)
	if err != nil {
		return "", false, err
	}
	if !s.skillMarkdownPreviewCurrent(state) {
		return "", false, fmt.Errorf("Skill 预览已切换，无法读取此图片")
	}
	return path, false, nil
}
