package desktop

import (
	"context"
	"errors"
	"os"

	"silverwing-go/internal/documents"
)

type documentLocalPreviewFileResolver struct {
	files *documents.Service
}

func newDocumentLocalPreviewFileResolver() localPreviewFileResolver {
	return documentLocalPreviewFileResolver{files: documents.NewService("")}
}

func (r documentLocalPreviewFileResolver) ResolveLocalFile(ctx context.Context, root, relativePath string) (string, error) {
	// Reuse the existing root, component, reparse and operation-scope checks.
	paths, err := r.files.ResolveCopyPaths(ctx, root, []string{relativePath})
	if err != nil {
		return "", err
	}
	if len(paths) != 1 {
		return "", errors.New("Skill 图片路径无效")
	}
	info, err := os.Lstat(paths[0])
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("Skill 图片必须是目录内的普通文件")
	}
	return paths[0], nil
}
