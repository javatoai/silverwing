//go:build !windows

package auth

import "os"

func createPrivateTemp(directory string) (*os.File, error) {
	file, err := os.CreateTemp(directory, ".snowball-auth-*.tmp")
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	return file, nil
}
