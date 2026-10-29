package desktop

import "context"

// localPreviewFileResolver authorizes one root-relative preview resource and
// returns a regular local file. Implementations must reject unsafe components,
// reparse aliases inside the root, and paths outside the captured root.
type localPreviewFileResolver interface {
	ResolveLocalFile(ctx context.Context, root, relativePath string) (string, error)
}
