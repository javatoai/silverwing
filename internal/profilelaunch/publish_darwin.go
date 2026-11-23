package profilelaunch

import "golang.org/x/sys/unix"

func publishShortcut(staged, target string, replace bool) error {
	if replace {
		// The previous bundle moves to the staging path for cleanup, while the
		// destination always points to a complete bundle.
		return unix.RenamexNp(staged, target, unix.RENAME_SWAP)
	}
	return unix.RenamexNp(staged, target, unix.RENAME_EXCL)
}
