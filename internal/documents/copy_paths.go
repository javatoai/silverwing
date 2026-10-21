package documents

import (
	"context"
	"os"
)

// ResolveCopyPaths validates the captured selection without collapsing a
// selected child into its selected parent or walking a directory's contents.
func (s *Service) ResolveCopyPaths(ctx context.Context, root string, values []string) ([]string, error) {
	validatedRoot, err := validatedMaterialsRoot(root)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(values))
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path, err := safePath(validatedRoot, value, false)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if err := validateMaterialPath(validatedRoot, path, info); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, ctx.Err()
}
