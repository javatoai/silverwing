package profilelaunch

import "golang.org/x/sys/windows"

func publishShortcut(staged, target string, replace bool) error {
	from, err := windows.UTF16PtrFromString(staged)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	var flags uint32
	if replace {
		flags = windows.MOVEFILE_REPLACE_EXISTING
	}
	return windows.MoveFileEx(from, to, flags)
}
