package desktop

import (
	"path/filepath"
	"sort"
	"strings"
)

type materialsFileSelectionEntry struct {
	Key       string
	Path      string
	Relative  string
	Directory bool
}

// materialsSelection owns document choice, independently from the active
// reader, native focus, filesystem operations, and permission decisions.
type materialsSelection struct {
	selected map[string]materialsFileSelectionEntry
	anchor   string
}

func (s *materialsSelection) ensure() {
	if s.selected == nil {
		s.selected = make(map[string]materialsFileSelectionEntry)
	}
}

func (s *materialsSelection) reset() {
	s.selected = make(map[string]materialsFileSelectionEntry)
	s.anchor = ""
}

func (s *materialsSelection) selectOnly(entry materialsFileSelectionEntry) {
	s.selected = map[string]materialsFileSelectionEntry{entry.Key: entry}
	s.anchor = entry.Key
}

func (s *materialsSelection) apply(entry materialsFileSelectionEntry, visible []materialsFileSelectionEntry, toggle, extend bool) {
	s.ensure()
	if extend && s.anchor != "" {
		from, to := -1, -1
		for index, item := range visible {
			if item.Key == s.anchor {
				from = index
			}
			if item.Key == entry.Key {
				to = index
			}
		}
		if from >= 0 && to >= 0 {
			if !toggle {
				s.selected = make(map[string]materialsFileSelectionEntry)
			}
			for _, item := range visible[min(from, to) : max(from, to)+1] {
				s.selected[item.Key] = item
			}
		} else if !toggle {
			s.selected = map[string]materialsFileSelectionEntry{entry.Key: entry}
		}
	} else if toggle {
		if _, selected := s.selected[entry.Key]; selected {
			delete(s.selected, entry.Key)
		} else {
			s.selected[entry.Key] = entry
		}
	} else {
		s.selected = map[string]materialsFileSelectionEntry{entry.Key: entry}
	}
	// Shift keeps even an empty or no-longer-visible anchor. Its fallback is
	// intentionally different from the generic file-tree gesture algorithm.
	if !extend {
		s.anchor = entry.Key
	}
}

func (s materialsSelection) operationEntries() []materialsFileSelectionEntry {
	if len(s.selected) == 0 {
		return nil
	}
	entries := make([]materialsFileSelectionEntry, 0, len(s.selected))
	for _, entry := range s.selected {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Relative) < strings.ToLower(entries[j].Relative)
	})
	selectedDirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Directory {
			selectedDirs = append(selectedDirs, filepath.Clean(entry.Path))
		}
	}
	filtered := entries[:0]
	for _, entry := range entries {
		covered := false
		for _, parent := range selectedDirs {
			if normalizedReadingPath(parent) == normalizedReadingPath(entry.Path) {
				continue
			}
			relative, err := filepath.Rel(parent, entry.Path)
			if err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
				covered = true
				break
			}
		}
		if !covered {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// Copying paths keeps selected descendants and uses the entry key order. File
// operations deliberately use operationEntries' different scope and ordering.
func (s materialsSelection) copyPaths() []string {
	keys := make([]string, 0, len(s.selected))
	for key := range s.selected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	paths := make([]string, 0, len(keys))
	for _, key := range keys {
		paths = append(paths, s.selected[key].Path)
	}
	return paths
}

func (s materialsSelection) contextEntries(entry materialsFileSelectionEntry) []materialsFileSelectionEntry {
	if _, selected := s.selected[entry.Key]; selected {
		return s.operationEntries()
	}
	return []materialsFileSelectionEntry{entry}
}

func (s materialsSelection) forContext(entry materialsFileSelectionEntry) materialsSelection {
	if _, contains := s.selected[entry.Key]; !contains {
		s.selectOnly(entry)
	}
	return s
}

func (s materialsSelection) currentDirectory(projection materialsDocumentProjection, defaultDirectory string) (absolute, relative string) {
	if _, anchorRelative, hasAnchor := strings.Cut(s.anchor, ":"); hasAnchor {
		anchorPath := filepath.Join(projection.root, filepath.FromSlash(anchorRelative))
		if anchor, ok := projection.find(anchorPath); ok && anchor.Key == s.anchor {
			relative = anchor.Relative
			if !anchor.Directory {
				relative = filepath.ToSlash(filepath.Dir(filepath.FromSlash(relative)))
				if relative == "." {
					relative = ""
				}
			}
		}
	}
	if relative == "" {
		return defaultDirectory, projection.relativePath(defaultDirectory)
	}
	absolute = filepath.Clean(filepath.Join(projection.root, filepath.FromSlash(relative)))
	rel, err := filepath.Rel(projection.root, absolute)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Clean(projection.root), ""
	}
	return absolute, filepath.ToSlash(rel)
}

func (s *materialsSelection) reconcile(projection materialsDocumentProjection) map[string]materialsFileSelectionEntry {
	available, anchorAvailable := projection.entryIndex(s.anchor)
	for key, entry := range s.selected {
		if current, ok := available[normalizedReadingPath(entry.Path)]; !ok || current.Directory != entry.Directory {
			delete(s.selected, key)
		} else {
			s.selected[key] = current
		}
	}
	if !anchorAvailable {
		s.anchor = ""
	}
	return available
}
