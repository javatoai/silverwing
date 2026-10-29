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
}

type materialsDocumentRow struct {
	entry materialsFileSelectionEntry
	name  string
	depth int
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
	var visit func([]application.Document) (materialsFileSelectionEntry, bool)
	visit = func(items []application.Document) (materialsFileSelectionEntry, bool) {
		for _, document := range items {
			if normalizedReadingPath(document.Path) == normalizedReadingPath(path) {
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
	available := make(map[string]materialsFileSelectionEntry)
	anchorAvailable := false
	var visit func([]application.Document)
	visit = func(items []application.Document) {
		for _, document := range items {
			if entry, ok := p.entry(document); ok {
				available[normalizedReadingPath(entry.Path)] = entry
				if entry.Key == anchor {
					anchorAvailable = true
				}
			}
			visit(document.Children)
		}
	}
	visit(p.documents)
	return available, anchorAvailable
}

func (p materialsDocumentProjection) treeRows(order string) []materialsDocumentRow {
	rows := make([]materialsDocumentRow, 0, len(p.documents))
	var visit func([]application.Document, int)
	visit = func(items []application.Document, depth int) {
		for _, document := range items {
			entry, ok := p.entry(document)
			if !ok {
				continue
			}
			rows = append(rows, materialsDocumentRow{entry: entry, name: document.Name, depth: depth})
			visit(document.Children, depth+1)
		}
	}
	visit(sortDocumentsByOrder(p.documents, order), 0)
	return rows
}

func (p materialsDocumentProjection) visibleEntries(order string, expanded map[string]bool) []materialsFileSelectionEntry {
	entries := make([]materialsFileSelectionEntry, 0, len(p.documents))
	var visit func([]application.Document)
	visit = func(items []application.Document) {
		for _, document := range items {
			entry, ok := p.entry(document)
			if !ok {
				continue
			}
			entries = append(entries, entry)
			if entry.Directory && expanded[entry.Relative] {
				visit(document.Children)
			}
		}
	}
	visit(sortDocumentsByOrder(p.documents, order))
	return entries
}

func (p materialsDocumentProjection) quickMatches(query string) []quickPickerEntry {
	if p.root == "" {
		return nil
	}
	query = strings.ToLower(strings.TrimSpace(query))
	matches := make([]quickPickerEntry, 0, 32)
	var visit func([]application.Document)
	visit = func(items []application.Document) {
		if len(matches) >= 200 {
			return
		}
		for _, document := range items {
			if len(matches) >= 200 {
				return
			}
			if isDocumentDirectory(document) {
				visit(document.Children)
				if len(matches) >= 200 {
					return
				}
				continue
			}
			// Quick-open historically accepts a file at relative "." and
			// keeps its original Path spelling, unlike selectable tree entries.
			relative, err := filepath.Rel(p.root, document.Path)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
				continue
			}
			relative = filepath.ToSlash(relative)
			if query != "" && !strings.Contains(strings.ToLower(relative), query) {
				continue
			}
			matches = append(matches, quickPickerEntry{path: document.Path, name: filepath.Base(document.Path), relative: relative})
		}
	}
	visit(p.documents)
	return matches
}

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
