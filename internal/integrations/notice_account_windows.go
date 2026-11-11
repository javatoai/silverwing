//go:build windows

package integrations

import (
	"encoding/base64"
	"errors"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"silverwing-go/internal/releasenotice"
)

// Meegle CLI v1 uses this per-profile DPAPI registry value. Read the encrypted
// representation only: Silverwing neither decrypts nor persists credentials.
func noticeNativeCredentialStamp(profile string) (string, bool) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\MeegleCli\keychain\meegle-cli`, registry.QUERY_VALUE)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return "", true
	}
	if err != nil {
		return "", false
	}
	defer key.Close()
	value, _, err := key.GetStringValue(base64.RawURLEncoding.EncodeToString([]byte(profile)))
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return "", true
	}
	if err != nil || value == "" {
		return "", false
	}
	return releasenotice.Digest(value), true
}
