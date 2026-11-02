//go:build windows

package workspace

import (
	"encoding/binary"
	"fmt"

	"golang.org/x/sys/windows"
)

const recoveryWSLSymlinkTag = 0xa000001d // IO_REPARSE_TAG_LX_SYMLINK

// WSL links appear as irregular files to Go on Windows. Preserve their reparse
// data instead of opening the Linux target, which may be dangling or external.
// Other special files remain unsupported: copying an arbitrary reparse record
// (for example a cloud placeholder) could silently lose its actual contents.
func copyIrregularRecoveryFile(source, target string) error {
	name, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fmt.Errorf("无法备份特殊文件 %s：%w", source, err)
	}
	data := make([]byte, windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE)
	var returned uint32
	err = windows.DeviceIoControl(handle, windows.FSCTL_GET_REPARSE_POINT,
		nil, 0, &data[0], uint32(len(data)), &returned, nil)
	_ = windows.CloseHandle(handle)
	if err != nil {
		return fmt.Errorf("无法备份特殊文件 %s：%w", source, err)
	}
	if returned < 12 || returned > uint32(len(data)) || binary.LittleEndian.Uint32(data) != recoveryWSLSymlinkTag ||
		uint32(binary.LittleEndian.Uint16(data[4:]))+8 != returned {
		return fmt.Errorf("不支持恢复特殊文件：%s", source)
	}
	name, err = windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	// CREATE_NEW refuses to overwrite or follow an existing destination link.
	handle, err = windows.CreateFile(name, windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return fmt.Errorf("无法备份 WSL 链接 %s：%w", source, err)
	}
	complete := false
	defer func() {
		_ = windows.CloseHandle(handle)
		if !complete {
			_ = windows.DeleteFile(name)
		}
	}()
	var written uint32
	if err := windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT,
		&data[0], returned, nil, 0, &written, nil); err != nil {
		return fmt.Errorf("无法备份 WSL 链接 %s：%w", source, err)
	}
	complete = true
	return nil
}
