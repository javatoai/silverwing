package desktop

import (
	"path"
	"sort"
	"strings"

	"silverwing-go/internal/workspace"
)

type workspaceReviewTreeDirectory struct {
	Name        string
	Path        string
	Directories map[string]*workspaceReviewTreeDirectory
	Files       []workspace.CompareChange
}

func workspaceReviewTree(files []workspace.CompareChange) *workspaceReviewTreeDirectory {
	root := &workspaceReviewTreeDirectory{Directories: make(map[string]*workspaceReviewTreeDirectory)}
	for _, file := range files {
		segments := workspaceReviewPathSegments(file.Path)
		if len(segments) == 0 {
			continue
		}
		directory := root
		for index, segment := range segments[:len(segments)-1] {
			dirPath := strings.Join(segments[:index+1], "/")
			next := directory.Directories[segment]
			if next == nil {
				next = &workspaceReviewTreeDirectory{
					Name: segment, Path: dirPath,
					Directories: make(map[string]*workspaceReviewTreeDirectory),
				}
				directory.Directories[segment] = next
			}
			directory = next
		}
		directory.Files = append(directory.Files, file)
	}
	workspaceReviewSortTree(root)
	return root
}

func workspaceReviewPathSegments(value string) []string {
	value = strings.Trim(strings.ReplaceAll(value, "\\", "/"), "/")
	if value == "" || value == "." {
		return nil
	}
	segments := strings.Split(path.Clean(value), "/")
	clean := segments[:0]
	for _, segment := range segments {
		if segment != "" && segment != "." && segment != ".." {
			clean = append(clean, segment)
		}
	}
	return clean
}

func workspaceReviewSortTree(directory *workspaceReviewTreeDirectory) {
	sort.SliceStable(directory.Files, func(i, j int) bool {
		return strings.ToLower(directory.Files[i].Path) < strings.ToLower(directory.Files[j].Path)
	})
	for _, child := range directory.Directories {
		workspaceReviewSortTree(child)
	}
}

func workspaceReviewSortedDirectories(directory *workspaceReviewTreeDirectory) []*workspaceReviewTreeDirectory {
	children := make([]*workspaceReviewTreeDirectory, 0, len(directory.Directories))
	for _, child := range directory.Directories {
		children = append(children, child)
	}
	sort.SliceStable(children, func(i, j int) bool {
		return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
	})
	return children
}

func workspaceReviewSortedFiles(files []workspace.CompareChange) []workspace.CompareChange {
	result := append([]workspace.CompareChange(nil), files...)
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(strings.ReplaceAll(result[i].Path, "\\", "/")) <
			strings.ToLower(strings.ReplaceAll(result[j].Path, "\\", "/"))
	})
	return result
}

func workspaceReviewBaseName(value string) string {
	segments := workspaceReviewPathSegments(value)
	if len(segments) == 0 {
		return value
	}
	return segments[len(segments)-1]
}

func workspaceReviewEnsureExpandedDirectories(state *workspaceHistoryState, files []workspace.CompareChange) {
	if state == nil {
		return
	}
	if state.ExpandedDirectories == nil {
		state.ExpandedDirectories = make(map[string]bool)
	}
	for _, file := range files {
		segments := workspaceReviewPathSegments(file.Path)
		for index := 1; index < len(segments); index++ {
			key := strings.Join(segments[:index], "/")
			if _, exists := state.ExpandedDirectories[key]; !exists {
				state.ExpandedDirectories[key] = true
			}
		}
	}
}
