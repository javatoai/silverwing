package application

import (
	"errors"
	"net/url"
	"strings"
)

// Keep URI recognition separate from native opening so file paths continue
// through the file association path and mail links reach the OS mail handler.
func systemOpenLink(path string) (bool, error) {
	parsed, err := url.Parse(path)
	if err != nil {
		return false, nil
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		if parsed.Host == "" {
			return false, errors.New("链接缺少主机")
		}
		return true, nil
	case "mailto":
		return true, nil
	default:
		return false, nil
	}
}
