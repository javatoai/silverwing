package native

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"silverwing-go/internal/appupdate"
)

func Detect(executable, home, publicKey string, identity appupdate.Identity) (appupdate.Installation, error) {
	var installation appupdate.Installation
	key, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(key) != 32 {
		return installation, errors.New("当前构建未配置更新签名，可手动下载安装")
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return installation, err
	}
	if real, e := filepath.EvalSymlinks(executable); e == nil {
		executable = real
	} else {
		return installation, e
	}
	root := filepath.Dir(executable)
	kind := appupdate.Portable
	marker := "resources/update-installation.json"
	if runtime.GOOS == "darwin" {
		root = filepath.Dir(filepath.Dir(root))
		if !strings.HasSuffix(root, ".app") {
			return installation, errors.New("仅完整的 macOS 应用支持自动更新")
		}
		kind, marker = appupdate.MacApp, "Contents/Resources/update-installation.json"
	} else if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return installation, errors.New("当前平台请手动下载安装")
	}
	if filepath.Dir(root) == root || appupdate.Within(root, home) {
		return installation, errors.New("数据目录位于程序目录内，请手动更新以保留资料")
	}
	path, err := appupdate.Resolve(root, marker)
	if err != nil {
		return installation, err
	}
	var layout appupdate.Layout
	if err := appupdate.ReadJSON(path, &layout); err != nil {
		return installation, errors.New("当前程序缺少发行清单，请手动升级一次")
	}
	if layout.Identity != identity {
		return installation, errors.New("程序清单与当前版本不一致")
	}
	managedExe, err := appupdate.Resolve(root, layout.Executable)
	if err != nil || !samePath(managedExe, executable) {
		return installation, errors.New("程序安装位置无法确认")
	}
	helper, err := appupdate.Resolve(root, layout.Helper)
	if err != nil {
		return installation, err
	}
	if _, err := os.Stat(helper); err != nil {
		return installation, errors.New("当前程序缺少更新助手")
	}
	probe, err := os.CreateTemp(filepath.Dir(root), ".silverwing-write-probe-")
	if err != nil {
		return installation, errors.New("无权写入程序安装目录，请手动下载安装")
	}
	probe.Close()
	os.Remove(probe.Name())
	installation = appupdate.Installation{Identity: identity, Kind: kind, Root: root, Executable: executable, Home: home, Helper: helper, LayoutPath: path, Layout: layout}
	if runtime.GOOS == "windows" {
		return detectWindows(installation)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if code, err := (Runner{}).Run(ctx, "/usr/bin/codesign", []string{"--verify", "--deep", "--strict", root}); err != nil || code != 0 {
		return installation, errors.New("当前 macOS 应用签名无法确认，请手动下载安装")
	}
	return installation, nil
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
