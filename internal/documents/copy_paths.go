package documents

import (
	"context"
	"os"
)

// ResolveCopyPaths validates the captured selection without collapsing a
// selected child into its selected parent or walking a directory's contents.
func (s *Service) ResolveCopyPaths(ctx context.Context, root string, values []string) (paths []string, resultErr error) {
	scope, err := newMaterialsOperationScope(root)
	if err != nil {
		return nil, err
	}
	defer scope.finishPaths(&paths, &resultErr)
	paths = make([]string, 0, len(values))
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path, err := scope.resolve(value, false, false, false)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if err := validateMaterialPath(scope.root, path, info); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, ctx.Err()
}
