package desktop

import (
	"net/url"
	"path/filepath"
	"strings"
)

type markdownLinkDestination struct {
	url      string
	path     string
	fragment string
}

// resolveMarkdownLinkDestination mirrors the source reader's link boundary:
// supported external schemes are handed to the operating system, while local
// destinations must remain below the document root. It deliberately does not
// interpret heading fragments; the current source renderer does not consume
// them either.
func resolveMarkdownLinkDestination(sourcePath, materialsRoot, destination string) (markdownLinkDestination, bool) {
	raw := strings.TrimSpace(destination)
	if raw == "" {
		return markdownLinkDestination{}, false
	}
	parsed, err := url.Parse(strings.ReplaceAll(raw, " ", "%20"))
	if err != nil {
		return markdownLinkDestination{}, false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto":
		return markdownLinkDestination{url: raw}, true
	}
	if parsed.Scheme != "" || parsed.Host != "" || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, `\`) || strings.HasPrefix(parsed.Path, "/") {
		return markdownLinkDestination{}, false
	}
	if sourcePath == "" || materialsRoot == "" {
		return markdownLinkDestination{}, false
	}

	root, err := filepath.Abs(materialsRoot)
	if err != nil {
		return markdownLinkDestination{}, false
	}
	source, err := filepath.Abs(sourcePath)
	if err != nil || !markdownLinkPathWithinRoot(root, source) {
		return markdownLinkDestination{}, false
	}
	if parsed.Path == "" && !strings.Contains(raw, "#") {
		return markdownLinkDestination{}, false
	}
	target := source
	if parsed.Path != "" {
		target, err = filepath.Abs(filepath.Join(filepath.Dir(source), filepath.FromSlash(parsed.Path)))
	}
	if err != nil || !markdownLinkPathWithinRoot(root, target) {
		return markdownLinkDestination{}, false
	}
	return markdownLinkDestination{path: target, fragment: parsed.Fragment}, true
}

func markdownLinkPathWithinRoot(root, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (s *app) bindSkillsMarkdownLinkNavigation(state *skillsBrowserState) {
	if s == nil || state == nil || state.markdown == nil || state.root == "" || state.selectedDirectory == "" || state.selectedPath == "" {
		return
	}
	markdown := state.markdown
	directory := state.selectedDirectory
	selectedPath := filepath.ToSlash(state.selectedPath)
	root, err := filepath.Abs(filepath.Join(state.root, directory))
	if err != nil {
		return
	}
	source, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(selectedPath)))
	if err != nil || !markdownLinkPathWithinRoot(root, source) {
		return
	}
	markdown.path = source
	markdown.linkRoot = root
	markdown.onNavigateLocalLink = func(relativePath, _ string) {
		if s.skillsBrowser != state || state.markdown != markdown || state.selectedDirectory != directory || filepath.ToSlash(state.selectedPath) != selectedPath {
			return
		}
		for _, file := range state.files {
			if filepath.ToSlash(mapString(file, "relativePath")) == relativePath && dataBool(file, "markdown") {
				s.selectBrowserFile(file)
				return
			}
		}
	}
}

func (s *app) openMarkdownLink(state *markdownEditorState, destination string) {
	if state == nil || state.closed {
		return
	}
	if target, ok := resolveMarkdownLinkDestination(state.path, "", destination); ok && target.url != "" {
		s.act("system.open", "", "", "", target.url, "", nil)
		return
	}
	if state.detached {
		if !state.readOnly || state.linkRoot == "" || state.onNavigateLocalLink == nil || state.path == "" {
			return
		}
		target, ok := resolveMarkdownLinkDestination(state.path, state.linkRoot, destination)
		if !ok || target.path == "" {
			return
		}
		relative, err := filepath.Rel(filepath.Clean(state.linkRoot), target.path)
		if err != nil || !markdownLinkPathWithinRoot(state.linkRoot, target.path) {
			return
		}
		state.onNavigateLocalLink(filepath.ToSlash(relative), target.fragment)
		return
	}
	if state.taskID == "" || state.taskID != s.currentTask().id || s.markdown != state || state.path != s.documentPath || s.busy {
		return
	}
	task := s.currentTask()
	target, ok := resolveMarkdownLinkDestination(state.path, task.materialsPath, destination)
	if !ok || target.path == "" {
		return
	}
	entry, ok := s.findMaterialsEntry(target.path, s.documents)
	if !ok || entry.Directory {
		return
	}
	s.selectMaterialsPath(entry.Path)
	s.documentLinkFragment = target.fragment
}
