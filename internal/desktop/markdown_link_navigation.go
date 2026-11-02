package desktop

import (
	"fmt"
	"net/url"
	"path/filepath"
	markdownui "silverwing-go/internal/ui/markdown"
	"strconv"
	"strings"
)

type markdownLinkDestination struct {
	url      string
	path     string
	fragment string
}

func markdownLinkLocation(fragment string) (line, column int) {
	if len(fragment) < 2 || fragment[0] != 'L' && fragment[0] != 'l' {
		return 0, 0
	}
	parts := strings.SplitN(fragment[1:], ":", 2)
	line, _ = strconv.Atoi(parts[0])
	if len(parts) > 1 {
		column, _ = strconv.Atoi(parts[1])
	}
	return
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
		if s.settingsUnit.state.skillsBrowser != state || state.markdown != markdown || state.selectedDirectory != directory || filepath.ToSlash(state.selectedPath) != selectedPath {
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
	resolved, err := markdownui.ResolveLink(state.path, destination)
	if err != nil {
		s.lastError = err.Error()
		return
	}
	if resolved.URL != "" {
		s.act("system.open", "", "", "", resolved.URL, "", nil)
		return
	}
	root := state.linkRoot
	if !state.detached {
		root = s.taskUnit.currentTask().materialsPath
	}
	if resolved.Path != "" && (root == "" || !markdownLinkPathWithinRoot(root, resolved.Path)) {
		s.openMarkdownFileLink(state, resolved)
		return
	}
	if state.detached && (state.onNavigateLocalLink == nil || state.linkRoot == "") {
		s.openMarkdownFileLink(state, resolved)
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
		target := markdownLinkDestination{path: resolved.Path, fragment: resolved.Fragment}
		ok := target.path != ""
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
	if state.taskID == "" || state.taskID != s.taskUnit.currentTask().id || s.materialsUnit.state.markdown != state || state.path != s.materialsUnit.state.documentPath || s.busy {
		return
	}
	task := s.taskUnit.currentTask()
	target := markdownLinkDestination{path: resolved.Path, fragment: resolved.Fragment}
	ok := target.path != "" && markdownLinkPathWithinRoot(task.materialsPath, resolved.Path)
	if !ok || target.path == "" {
		return
	}
	entry, ok := s.findMaterialsEntry(target.path, s.materialsUnit.state.documents)
	if !ok || entry.Directory {
		s.openMarkdownFileLink(state, resolved)
		return
	}
	s.selectMaterialsPath(entry.Path)
	s.materialsUnit.state.documentLinkFragment = target.fragment
	if resolved.Line > 0 {
		s.materialsUnit.state.documentLinkFragment = fmt.Sprintf("L%d:%d", resolved.Line, resolved.Column)
	}
}
