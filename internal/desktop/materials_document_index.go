package desktop

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/application"
)

type materialsDocumentProjectionKey struct {
	generation                        uint64
	taskID, taskPath, createdAt, root string
}

// One feature owns one catalog. These are detached value projections, never a
// process cache or a second source of document/permission authorization.
type materialsDocumentProjectionCache struct {
	key   materialsDocumentProjectionKey
	index *materialsDocumentIndex
}

func (c *materialsDocumentProjectionCache) project(key materialsDocumentProjectionKey, documents []application.Document) materialsDocumentProjection {
	if c.index == nil || c.key != key {
		c.key = key
		c.index = newMaterialsDocumentIndex(key.root, documents)
	}
	return materialsDocumentProjection{root: key.root, index: c.index}
}

type materialsDocumentLookup struct {
	entry materialsFileSelectionEntry
	valid bool
}

type materialsDocumentNode struct {
	lookup                          materialsDocumentLookup
	name, lowerName, path, modified string
	modifiedTime                    time.Time
	modifiedValid                   bool
	directory                       bool
	children                        []materialsDocumentNode
}

type materialsQuickIndexEntry struct {
	entry         quickPickerEntry
	lowerRelative string
}

type materialsDocumentIndex struct {
	root        string
	count       int
	nodes       []materialsDocumentNode
	paths       map[string]materialsDocumentLookup
	entries     map[string]materialsFileSelectionEntry
	keys        map[string]bool
	quick       []materialsQuickIndexEntry
	ordered     [2]*materialsDocumentRows
	quickQuery  string
	quickResult []quickPickerEntry
	quickValid  bool
}

func newMaterialsDocumentIndex(root string, documents []application.Document) *materialsDocumentIndex {
	index := &materialsDocumentIndex{root: root, paths: make(map[string]materialsDocumentLookup), entries: make(map[string]materialsFileSelectionEntry), keys: make(map[string]bool)}
	projection := materialsDocumentProjection{root: root}
	var collect func([]application.Document, bool) []materialsDocumentNode
	collect = func(items []application.Document, quickVisible bool) []materialsDocumentNode {
		nodes := make([]materialsDocumentNode, 0, len(items))
		for _, document := range items {
			index.count++
			entry, valid := projection.entry(document)
			lookup := materialsDocumentLookup{entry: entry, valid: valid}
			pathKey := normalizedReadingPath(document.Path)
			if _, found := index.paths[pathKey]; !found {
				index.paths[pathKey] = lookup
			}
			if valid {
				index.entries[normalizedReadingPath(entry.Path)] = entry
				index.keys[entry.Key] = true
			}
			directory := isDocumentDirectory(document)
			if quickVisible && !directory && root != "" {
				// Keep quick-open's original path spelling and relative-dot rule.
				relative, err := filepath.Rel(root, document.Path)
				if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
					relative = filepath.ToSlash(relative)
					index.quick = append(index.quick, materialsQuickIndexEntry{entry: quickPickerEntry{path: document.Path, name: filepath.Base(document.Path), relative: relative}, lowerRelative: strings.ToLower(relative)})
				}
			}
			modifiedTime, modifiedErr := time.Parse(time.RFC3339Nano, document.Modified)
			nodes = append(nodes, materialsDocumentNode{
				lookup: lookup, name: document.Name, lowerName: strings.ToLower(document.Name), path: document.Path,
				modified: document.Modified, modifiedTime: modifiedTime, modifiedValid: modifiedErr == nil, directory: directory,
				children: collect(document.Children, quickVisible && directory),
			})
		}
		return nodes
	}
	index.nodes = collect(documents, true)
	return index
}

func materialsDocumentOrderIndex(order string) int {
	if order == "MODIFIED_DESC" {
		return 1
	}
	return 0
}

func (index *materialsDocumentIndex) orderedRows(order string) *materialsDocumentRows {
	orderIndex := materialsDocumentOrderIndex(order)
	if cached := index.ordered[orderIndex]; cached != nil {
		return cached
	}
	result := &materialsDocumentRows{positions: make(map[string]int)}
	var flatten func([]materialsDocumentNode, int, int)
	flatten = func(items []materialsDocumentNode, depth, parent int) {
		ordered := make([]int, len(items))
		for i := range ordered {
			ordered[i] = i
		}
		sort.SliceStable(ordered, func(i, j int) bool {
			left, right := &items[ordered[i]], &items[ordered[j]]
			leftDirectory, rightDirectory := left.directory, right.directory
			if leftDirectory != rightDirectory {
				return leftDirectory
			}
			if orderIndex == 1 && left.modified != right.modified {
				if left.modifiedValid && right.modifiedValid && !left.modifiedTime.Equal(right.modifiedTime) {
					return left.modifiedTime.After(right.modifiedTime)
				}
				return left.modified > right.modified
			}
			if strings.EqualFold(left.name, right.name) {
				return left.path < right.path
			}
			return left.lowerName < right.lowerName
		})
		for _, itemIndex := range ordered {
			node := &items[itemIndex]
			if !node.lookup.valid {
				continue
			}
			position := len(result.rows)
			result.rows = append(result.rows, materialsDocumentRow{entry: node.lookup.entry, name: node.name, depth: depth, parent: parent})
			if _, exists := result.positions[node.lookup.entry.Key]; !exists {
				result.positions[node.lookup.entry.Key] = position
			}
			flatten(node.children, depth+1, position)
			result.rows[position].end = len(result.rows)
		}
	}
	flatten(index.nodes, 0, -1)
	index.ordered[orderIndex] = result
	return result
}

func (index *materialsDocumentIndex) quickMatches(query string) []quickPickerEntry {
	if index.root == "" {
		return nil
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if index.quickValid && index.quickQuery == query {
		return index.quickResult
	}
	matches := make([]quickPickerEntry, 0, min(32, len(index.quick)))
	for _, candidate := range index.quick {
		if query == "" || strings.Contains(candidate.lowerRelative, query) {
			matches = append(matches, candidate.entry)
			if len(matches) == 200 {
				break
			}
		}
	}
	index.quickQuery, index.quickResult, index.quickValid = query, matches, true
	return matches
}

// Only visible directory booleans affect this projection. Changes below a
// collapsed parent are picked up when that parent becomes visible/expanded.
// end skips a collapsed subtree without inspecting its descendants.
type materialsDocumentRows struct {
	rows                []materialsDocumentRow
	positions           map[string]int
	treeVisibility      materialsDocumentVisibility
	selectionVisibility materialsDocumentVisibility
}

type materialsDocumentVisibility struct {
	indices  []int
	entries  []materialsFileSelectionEntry
	expanded map[string]bool
	valid    bool
	revision uint64
}

func (v *materialsDocumentVisibility) project(rows []materialsDocumentRow, expanded map[string]bool, includeFileChildren bool) {
	valid := v.valid
	if valid {
		for relative, before := range v.expanded {
			if expanded[relative] != before {
				valid = false
				break
			}
		}
	}
	if valid {
		return
	}
	v.indices = make([]int, 0)
	v.entries = make([]materialsFileSelectionEntry, 0)
	v.expanded = make(map[string]bool)
	for i := 0; i < len(rows); {
		row := &rows[i]
		v.indices = append(v.indices, i)
		v.entries = append(v.entries, row.entry)
		if row.entry.Directory {
			v.expanded[row.entry.Relative] = expanded[row.entry.Relative]
		}
		if row.entry.Directory && !expanded[row.entry.Relative] || !row.entry.Directory && !includeFileChildren {
			i = row.end
		} else {
			i++
		}
	}
	v.valid = true
	v.revision++
}

func (r *materialsDocumentRows) visibleEntries(expanded map[string]bool) []materialsFileSelectionEntry {
	r.selectionVisibility.project(r.rows, expanded, false)
	return r.selectionVisibility.entries
}
