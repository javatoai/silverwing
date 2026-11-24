//go:build !windows

package platform

import "context"

func addCLIUserPath(_ context.Context, _ string) (string, error) {
	return "", nil
}
