//go:build !windows && !darwin

package desktop

import "context"

// Source ships Windows and macOS desktop packages. Other platforms retain
// the activation fallback without starting a filesystem polling loop.
func watchMaterialsDirectory(ctx context.Context, root string, changed func(string), ready func()) error {
	ready()
	<-ctx.Done()
	return ctx.Err()
}
