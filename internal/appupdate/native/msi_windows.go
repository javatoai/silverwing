//go:build windows

package native

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"silverwing-go/internal/appupdate"
	"silverwing-go/internal/productprofile"
)

func MSIFileIdentity(path, version string) (product, upgrade string, err error) {
	name, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return "", "", e
	}
	var database uint32
	status, _, _ := installerAPI.NewProc("MsiOpenDatabaseW").Call(uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&database)))
	if status != 0 {
		return "", "", fmt.Errorf("无法读取 MSI（%d）", status)
	}
	defer installerAPI.NewProc("MsiCloseHandle").Call(uintptr(database))
	property := func(key string) (string, error) {
		query, _ := windows.UTF16PtrFromString("SELECT `Value` FROM `Property` WHERE `Property`='" + key + "'")
		var view uint32
		status, _, _ := installerAPI.NewProc("MsiDatabaseOpenViewW").Call(uintptr(database), uintptr(unsafe.Pointer(query)), uintptr(unsafe.Pointer(&view)))
		if status != 0 {
			return "", errors.New("MSI 属性不可用")
		}
		defer installerAPI.NewProc("MsiCloseHandle").Call(uintptr(view))
		status, _, _ = installerAPI.NewProc("MsiViewExecute").Call(uintptr(view), 0)
		if status != 0 {
			return "", errors.New("MSI 属性查询失败")
		}
		var record uint32
		status, _, _ = installerAPI.NewProc("MsiViewFetch").Call(uintptr(view), uintptr(unsafe.Pointer(&record)))
		if status != 0 {
			return "", errors.New("MSI 属性不存在")
		}
		defer installerAPI.NewProc("MsiCloseHandle").Call(uintptr(record))
		buffer := make([]uint16, 1024)
		size := uint32(len(buffer))
		status, _, _ = installerAPI.NewProc("MsiRecordGetStringW").Call(uintptr(record), 1, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
		if status != 0 {
			return "", errors.New("MSI 属性读取失败")
		}
		return windows.UTF16ToString(buffer), nil
	}
	product, err = property("ProductCode")
	if err != nil {
		return
	}
	upgrade, err = property("UpgradeCode")
	if err != nil {
		return
	}
	actual, e := property("ProductVersion")
	product, upgrade = appupdate.CanonicalGUID(product), appupdate.CanonicalGUID(upgrade)
	if e != nil || actual != version || upgrade != appupdate.CanonicalGUID(productprofile.WindowsUpgradeCode) || product == "" {
		return "", "", errors.New("MSI 产品或版本与当前发行不一致")
	}
	return product, upgrade, nil
}
