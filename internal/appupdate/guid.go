package appupdate

import (
	"encoding/hex"
	"strings"
)

// CanonicalGUID returns the representation required by Windows Installer.
func CanonicalGUID(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}") {
		value = value[1 : len(value)-1]
	}
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return ""
	}
	if _, err := hex.DecodeString(strings.ReplaceAll(value, "-", "")); err != nil {
		return ""
	}
	return "{" + strings.ToUpper(value) + "}"
}
