//go:build darwin && !ios

package systemtrash

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var (
	foundationOnce sync.Once
	foundationErr  error

	selAlloc                     = objc.RegisterName("alloc")
	selInit                      = objc.RegisterName("init")
	selDrain                     = objc.RegisterName("drain")
	selDefaultManager            = objc.RegisterName("defaultManager")
	selFileURLWithRepresentation = objc.RegisterName("fileURLWithFileSystemRepresentation:isDirectory:relativeToURL:")
	selTrashItem                 = objc.RegisterName("trashItemAtURL:resultingItemURL:error:")
	selIsFileURL                 = objc.RegisterName("isFileURL")
	selFileSystemRepresentation  = objc.RegisterName("fileSystemRepresentation")
	selDomain                    = objc.RegisterName("domain")
	selCode                      = objc.RegisterName("code")
	selLocalizedDescription      = objc.RegisterName("localizedDescription")
	selUTF8String                = objc.RegisterName("UTF8String")
)

func move(ctx context.Context, path string) (string, error) {
	foundationOnce.Do(func() {
		_, foundationErr = purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	})
	if foundationErr != nil {
		return "", fmt.Errorf("无法加载 Foundation：%w", foundationErr)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	poolClass, managerClass, urlClass := objc.GetClass("NSAutoreleasePool"), objc.GetClass("NSFileManager"), objc.GetClass("NSURL")
	if poolClass == 0 || managerClass == 0 || urlClass == 0 {
		return "", errors.New("Foundation 文件回收接口不可用")
	}
	pool := objc.Send[objc.ID](objc.ID(poolClass), selAlloc)
	pool = objc.Send[objc.ID](pool, selInit)
	if pool == 0 {
		return "", errors.New("无法创建 Foundation 自动释放池")
	}
	defer objc.Send[objc.ID](pool, selDrain)
	manager := objc.Send[objc.ID](objc.ID(managerClass), selDefaultManager)
	source := objc.Send[objc.ID](objc.ID(urlClass), selFileURLWithRepresentation, path, info.IsDir(), objc.ID(0))
	if manager == 0 || source == 0 {
		return "", errors.New("无法创建 Foundation 文件回收请求")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var resultURL, nativeError objc.ID
	if objc.Send[uint8](manager, selTrashItem, source, &resultURL, &nativeError) == 0 {
		if nativeError == 0 {
			return "", errors.New("系统未能将项目移入废纸篓")
		}
		domain := objc.Send[objc.ID](nativeError, selDomain)
		description := objc.Send[objc.ID](nativeError, selLocalizedDescription)
		code := objc.Send[int](nativeError, selCode)
		return "", fmt.Errorf("%s（%s %d）", objc.Send[string](description, selUTF8String), objc.Send[string](domain, selUTF8String), code)
	}
	if resultURL == 0 || objc.Send[uint8](resultURL, selIsFileURL) == 0 {
		return "", errors.New("系统未返回有效的废纸篓文件地址")
	}
	result := objc.Send[string](resultURL, selFileSystemRepresentation)
	if !filepath.IsAbs(result) || filepath.Clean(result) == path {
		return "", errors.New("系统未返回有效的废纸篓文件路径")
	}
	return filepath.Clean(result), nil
}
