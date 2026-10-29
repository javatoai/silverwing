package desktop

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/application"
)

// materialsDocumentProjection reads an already-authorized document snapshot.
// It preserves the supplied path namespace and never consults the filesystem.
type materialsDocumentProjection struct {
	root      string
	documents []application.Document
	index     *materialsDocumentIndex
}

type materialsDocumentRow struct {
	entry       materialsFileSelectionEntry
	name        string
	depth       int
	parent, end int
}

type quickPickerEntry struct {
	path, name, relative string
}

func (p materialsDocumentProjection) relativePath(path string) string {
	if p.root == "" || path == "" {
		return ""
	}
	relative, err := filepath.Rel(p.root, path)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(relative)
}

func materialsFileEntryKey(relative string, directory bool) string {
	prefix := "file:"
	if directory {
		prefix = "directory:"
	}
	return prefix + filepath.ToSlash(relative)
}

func (p materialsDocumentProjection) entry(document application.Document) (materialsFileSelectionEntry, bool) {
	relative := p.relativePath(document.Path)
	if relative == "" {
		return materialsFileSelectionEntry{}, false
	}
	directory := isDocumentDirectory(document)
	return materialsFileSelectionEntry{Key: materialsFileEntryKey(relative, directory), Path: filepath.Clean(document.Path), Relative: relative, Directory: directory}, true
}

func (p materialsDocumentProjection) find(path string) (materialsFileSelectionEntry, bool) {
	pathKey := normalizedReadingPath(path)
	if p.index != nil {
		match := p.index.paths[pathKey]
		return match.entry, match.valid
	}
	// Standalone callers supply a live, arbitrary slice. Preserve its DFS first
	// match without building unrelated tree, quick-picker or timestamp indexes.
	var visit func([]application.Document) (materialsFileSelectionEntry, bool)
	visit = func(items []application.Document) (materialsFileSelectionEntry, bool) {
		for _, document := range items {
			if normalizedReadingPath(document.Path) == pathKey {
				return p.entry(document)
			}
			if entry, ok := visit(document.Children); ok {
				return entry, true
			}
		}
		return materialsFileSelectionEntry{}, false
	}
	return visit(p.documents)
}

// Catalog reconciliation visits children even when their parent cannot be an
// entry. Tree presentation and visible-range selection retain their own walk.
func (p materialsDocumentProjection) entryIndex(anchor string) (map[string]materialsFileSelectionEntry, bool) {
	index := p.documentIndex()
	return index.entries, index.keys[anchor]
}

func (p materialsDocumentProjection) treeRows(order string) []materialsDocumentRow {
	return p.documentIndex().orderedRows(order).rows
}

func (p materialsDocumentProjection) visibleEntries(order string, expanded map[string]bool) []materialsFileSelectionEntry {
	return p.documentIndex().orderedRows(order).visibleEntries(expanded)
}

func (p materialsDocumentProjection) quickMatches(query string) []quickPickerEntry {
	return p.documentIndex().quickMatches(query)
}

func (p materialsDocumentProjection) documentIndex() *materialsDocumentIndex {
	if p.index != nil {
		return p.index
	}
	// Standalone values do not cache a borrowed slice: callers can replace an
	// element or Children between calls. Controller snapshots use generation.
	return newMaterialsDocumentIndex(p.root, p.documents)
}

func (p materialsDocumentProjection) count() int { return p.documentIndex().count }

func isDocumentDirectory(document application.Document) bool {
	return document.Kind == "directory" || document.Kind == "dir" || document.Kind == "folder"
}

func sortDocuments(items []application.Document) {
	sorted := sortDocumentsByOrder(items, "NAME")
	copy(items, sorted)
}

func sortDocumentsByOrder(items []application.Document, order string) []application.Document {
	sorted := make([]application.Document, len(items))
	copy(sorted, items)
	for index := range sorted {
		sorted[index].Children = sortDocumentsByOrder(sorted[index].Children, order)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		left, right := sorted[i], sorted[j]
		leftDirectory, rightDirectory := isDocumentDirectory(left), isDocumentDirectory(right)
		if leftDirectory != rightDirectory {
			return leftDirectory
		}
		if order == "MODIFIED_DESC" && left.Modified != right.Modified {
			leftTime, leftErr := time.Parse(time.RFC3339Nano, left.Modified)
			rightTime, rightErr := time.Parse(time.RFC3339Nano, right.Modified)
			if leftErr == nil && rightErr == nil && !leftTime.Equal(rightTime) {
				return leftTime.After(rightTime)
			}
			return left.Modified > right.Modified
		}
		if strings.EqualFold(left.Name, right.Name) {
			return left.Path < right.Path
		}
		return strings.ToLower(left.Name) < strings.ToLower(right.Name)
	})
	return sorted
}
