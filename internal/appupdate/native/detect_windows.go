//go:build windows

package native

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"silverwing-go/internal/appupdate"
	"silverwing-go/internal/productprofile"
)

var installerAPI = windows.NewLazySystemDLL("msi.dll")
var enumRelatedProducts = installerAPI.NewProc("MsiEnumRelatedProductsW")
var getProductInfo = installerAPI.NewProc("MsiGetProductInfoW")

func detectWindows(i appupdate.Installation) (appupdate.Installation, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\`+productprofile.WindowsRegistryNamespace+`\Installer`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err == nil {
		installed, _, _ := key.GetIntegerValue("installed")
		key.Close()
		if installed == 1 {
			upgradeCode := appupdate.CanonicalGUID(productprofile.WindowsUpgradeCode)
			if upgradeCode == "" {
				return i, errors.New("MSI 产品标识无效")
			}
			upgrade, _ := windows.UTF16PtrFromString(upgradeCode)
			for index := uint32(0); index < 32; index++ {
				var code [39]uint16
				status, _, _ := enumRelatedProducts.Call(uintptr(unsafe.Pointer(upgrade)), 0, uintptr(index), uintptr(unsafe.Pointer(&code[0])))
				if status == 259 {
					break
				}
				if status != 0 {
					return i, fmt.Errorf("读取 MSI 安装登记失败（%d）", status)
				}
				product := windows.UTF16ToString(code[:])
				location, e := productInfo(product, "InstallLocation")
				if e != nil || !samePath(location, i.Root) {
					continue
				}
				version, e := productInfo(product, "VersionString")
				if e != nil || version != i.Identity.Version {
					return i, errors.New("MSI 登记版本与当前程序不一致")
				}
				i.Kind, i.ProductCode, i.UpgradeCode = appupdate.MSI, appupdate.CanonicalGUID(product), upgradeCode
				return i, nil
			}
			return i, errors.New("MSI 安装位置尚未登记，请手动升级一次")
		}
	}
	key, err = registry.OpenKey(registry.CURRENT_USER, `Software\`+productprofile.WindowsRegistryNamespace, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err == nil {
		path, _, e := key.GetStringValue("InstallDir")
		key.Close()
		if e == nil && samePath(path, i.Root) {
			i.Kind = appupdate.Setup
		}
	}
	return i, nil
}

func productInfo(product, property string) (string, error) {
	code, _ := syscall.UTF16PtrFromString(product)
	name, _ := syscall.UTF16PtrFromString(property)
	var size uint32
	status, _, _ := getProductInfo.Call(uintptr(unsafe.Pointer(code)), uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&size)))
	if status != 0 && status != 234 {
		return "", fmt.Errorf("MSI 属性不可用（%d）", status)
	}
	buffer := make([]uint16, size+1)
	size++
	status, _, _ = getProductInfo.Call(uintptr(unsafe.Pointer(code)), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
	if status != 0 {
		return "", fmt.Errorf("MSI 属性读取失败（%d）", status)
	}
	return windows.UTF16ToString(buffer), nil
}
