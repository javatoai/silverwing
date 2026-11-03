package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/appupdate"
)

type Runner interface {
	Run(context.Context, string, []string) (int, error)
}

type Registered struct {
	Kind   appupdate.Kind
	Runner Runner
}

var ErrSystemRestartRequired = errors.New("安装需要系统重启；未自动重启电脑，请手动完成安装")

func (i Registered) Apply(ctx context.Context, p appupdate.Plan) error {
	return i.run(ctx, p, p.New)
}

func (i Registered) Rollback(ctx context.Context, p appupdate.Plan) error {
	if p.Recovery == nil {
		return errors.New("缺少恢复安装包")
	}
	if i.Kind == appupdate.MSI {
		// Windows Installer owns the registration. Never restore just MSI files.
		code, err := i.Runner.Run(ctx, "msiexec.exe", []string{"/x", p.New.Metadata.ProductCode, "/qn", "/norestart"})
		if err != nil || code != 0 && code != 1605 && code != 3010 {
			return fmt.Errorf("移除未完成的 MSI 更新失败（%d）：%v", code, err)
		}
	}
	if err := i.run(ctx, p, *p.Recovery); err != nil {
		return err
	}
	if i.Kind == appupdate.Setup {
		return removeNewSetupFiles(p)
	}
	return nil
}

func (i Registered) run(ctx context.Context, p appupdate.Plan, bundle appupdate.Bundle) error {
	path, err := payload(bundle)
	if err != nil {
		return err
	}
	if bundle.Metadata.Kind != i.Kind {
		return errors.New("更新安装形式不匹配")
	}
	hash, err := appupdate.Digest(path)
	if err != nil || hash != bundle.Metadata.InstallerSHA256 {
		return errors.New("安装包校验失败")
	}
	var code int
	if i.Kind == appupdate.MSI {
		if appupdate.CanonicalGUID(bundle.Metadata.ProductCode) == "" || appupdate.CanonicalGUID(bundle.Metadata.UpgradeCode) == "" || appupdate.CanonicalGUID(bundle.Metadata.UpgradeCode) != appupdate.CanonicalGUID(p.Installation.UpgradeCode) {
			return errors.New("MSI 产品身份不匹配")
		}
		code, err = i.Runner.Run(ctx, "msiexec.exe", []string{"/i", path, "/qn", "/norestart", "INSTALLFOLDER=" + p.Installation.Root, "REBOOT=ReallySuppress"})
	} else {
		// The Setup installation reads its existing, verified InstallDir itself.
		code, err = i.Runner.Run(ctx, path, []string{"/S"})
	}
	if err != nil {
		return err
	}
	if code == 3010 {
		return ErrSystemRestartRequired
	}
	if code != 0 {
		return fmt.Errorf("安装器退出码 %d", code)
	}
	if bundle.Metadata.Layout == nil {
		return errors.New("安装包缺少安装后校验清单")
	}
	return appupdate.VerifyLayout(filepath.Clean(p.Installation.Root), *bundle.Metadata.Layout)
}

func removeNewSetupFiles(p appupdate.Plan) error {
	if p.New.Metadata.Layout == nil || p.Recovery == nil || p.Recovery.Metadata.Layout == nil {
		return errors.New("缺少 Setup 恢复文件清单")
	}
	old := make(map[string]bool)
	for _, f := range p.Recovery.Metadata.Layout.Files {
		old[strings.ToLower(f.Path)] = true
	}
	for _, f := range p.New.Metadata.Layout.Files {
		if old[strings.ToLower(f.Path)] {
			continue
		}
		path, err := appupdate.Resolve(p.Installation.Root, f.Path)
		if err != nil {
			return err
		}
		hash, err := appupdate.Digest(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || hash != f.SHA256 {
			return fmt.Errorf("新增程序文件已变化，未移除：%s", f.Path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
