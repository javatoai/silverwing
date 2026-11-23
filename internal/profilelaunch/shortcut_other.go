//go:build !windows && !darwin

package profilelaunch

import "errors"

func shortcutExtension() string { return "" }

func writeNativeShortcut(string, Shortcut) error {
	return errors.New("native profile shortcuts are supported on Windows and macOS")
}
