//go:build !windows

package native

import "errors"

func MSIFileIdentity(path, version string) (string, string, error) {
	return "", "", errors.New("MSI 只能在 Windows 上构建和验证")
}
