package native

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/egoist/mygo"
	"silverwing-go/internal/appupdate"
	"silverwing-go/internal/appupdate/install"
	"silverwing-go/internal/buildinfo"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/productprofile"
	"silverwing-go/internal/storage"
)

func Identity() appupdate.Identity {
	return appupdate.Identity{Repository: productprofile.ReleaseRepository, TagPrefix: productprofile.ReleaseTagPrefix, Executable: productprofile.ExecutableName, Platform: runtime.GOOS + "/" + runtime.GOARCH, Version: buildinfo.Version}
}

func Cache(home string) string {
	return filepath.Join(storage.NewPaths(home).Cache, "application-updates")
}

func RunHelper(ctx context.Context, planPath string) (returnErr error) {
	var plan appupdate.Plan
	if err := appupdate.ReadJSON(planPath, &plan); err != nil {
		return err
	}
	decoded, err := hex.DecodeString(plan.ID)
	if err != nil || len(decoded) != 16 || plan.Schema != 1 || plan.ParentPID <= 0 || plan.ParentPID == os.Getpid() || plan.HealthToken != plan.ID {
		return errors.New("无效的更新交接")
	}
	job := filepath.Dir(planPath)
	if !appupdate.Within(Cache(plan.Installation.Home), job) || filepath.Dir(job) != Cache(plan.Installation.Home) || filepath.Base(planPath) != "plan.json" {
		return errors.New("更新交接不在应用缓存内")
	}
	if plan.Installation.Identity != Identity() {
		return errors.New("更新助手与当前程序身份不一致")
	}
	if !appupdate.Within(job, plan.Backup) || filepath.Clean(plan.Backup) != filepath.Join(job, "backup") {
		return errors.New("无效的更新恢复目录")
	}
	var restart *appupdate.Installation
	suppressRestart := false
	defer func() {
		if returnErr != nil {
			path := filepath.Join(job, "result.json")
			_ = appupdate.WriteJSON(path, Outcome{Version: plan.New.Metadata.Identity.Version, Error: returnErr.Error(), RestartRequired: errors.Is(returnErr, install.ErrSystemRestartRequired)})
			if restart != nil && !suppressRestart {
				_, _ = StartWithOutcome(*restart, path)
			}
		}
	}()
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	err = WaitParent(waitCtx, plan.ParentPID)
	cancel()
	if err != nil {
		return fmt.Errorf("原程序尚未正常退出，未安装更新：%w", err)
	}
	unlock, err := Lock(plan.Installation.Root)
	if err != nil {
		return err
	}
	defer unlock()
	if err := OtherInstances(plan.Installation.Executable); err != nil {
		return err
	}
	current, err := Detect(plan.Installation.Executable, plan.Installation.Home, buildinfo.UpdatePublicKey, Identity())
	if err != nil {
		return err
	}
	if current.Kind != plan.Installation.Kind || !samePath(current.Root, plan.Installation.Root) || current.ProductCode != plan.Installation.ProductCode {
		return errors.New("程序安装位置或安装形式已变化")
	}
	if hash, e := appupdate.Digest(current.LayoutPath); e != nil || hash != plan.LayoutDigest {
		return errors.New("程序清单在下载后已变化")
	}
	if hash, e := appupdate.Digest(current.Executable); e != nil || hash != plan.ExecutableDigest {
		return errors.New("主程序在下载后已变化")
	}
	if current.Kind != appupdate.MacApp {
		if err := appupdate.VerifyLayout(current.Root, current.Layout); err != nil {
			return err
		}
	}
	restart = &current
	newBundle, err := verifiedBundle(job, plan.New, current, plan.New.Metadata.Identity.Version, "verified-new")
	if err != nil {
		return err
	}
	plan.New = newBundle
	if compare, valid := domain.CompareReleaseVersions(plan.New.Metadata.Identity.Version, current.Identity.Version); !valid || compare <= 0 {
		return errors.New("拒绝安装旧版或无法识别的版本")
	}
	if plan.Recovery != nil {
		recovery, e := verifiedBundle(job, *plan.Recovery, current, current.Identity.Version, "verified-recovery")
		if e != nil {
			return e
		}
		plan.Recovery = &recovery
		if current.Kind == appupdate.MSI && recovery.Metadata.ProductCode != current.ProductCode {
			return errors.New("MSI 恢复包与原安装登记不一致")
		}
	}
	var installer appupdate.Installer
	switch current.Kind {
	case appupdate.Portable:
		installer = install.Portable{}
	case appupdate.Setup, appupdate.MSI:
		installer = install.Registered{Kind: current.Kind, Runner: Runner{}}
	case appupdate.MacApp:
		installer = install.Mac{Runner: Runner{}}
	default:
		return errors.New("不支持当前安装形式")
	}
	if err := installer.Apply(ctx, plan); err != nil {
		if errors.Is(err, install.ErrSystemRestartRequired) {
			return err
		}
		rollbackErr := installer.Rollback(ctx, plan)
		suppressRestart = rollbackErr != nil
		return errors.Join(fmt.Errorf("更新安装失败：%w", err), rollbackErr)
	}
	health := filepath.Join(job, "healthy")
	started, err := Start(current, health, plan.HealthToken)
	if err == nil {
		suppressRestart = true
		readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		alive, readyErr := started.WaitHealthy(readyCtx, health, plan.HealthToken)
		cancel()
		if readyErr == nil {
			return appupdate.WriteJSON(filepath.Join(job, "result.json"), Outcome{Version: plan.New.Metadata.Identity.Version, Success: true})
		}
		if alive {
			suppressRestart = true
			return fmt.Errorf("新版已安装但尚未确认启动，恢复包已保留：%w", readyErr)
		}
		suppressRestart = false
		err = readyErr
	}
	rollbackErr := installer.Rollback(ctx, plan)
	suppressRestart = rollbackErr != nil
	return errors.Join(fmt.Errorf("新版启动失败：%w", err), rollbackErr)
}

type Outcome struct {
	Version         string `json:"version"`
	Success         bool   `json:"success,omitempty"`
	Error           string `json:"error,omitempty"`
	RestartRequired bool   `json:"restartRequired,omitempty"`
}

func ReadOutcome(home, path string) (Outcome, error) {
	var result Outcome
	if !appupdate.Within(Cache(home), path) || filepath.Base(path) != "result.json" {
		return result, errors.New("更新结果不在应用缓存内")
	}
	err := appupdate.ReadJSON(path, &result)
	return result, err
}

func verifiedBundle(job string, bundle appupdate.Bundle, installation appupdate.Installation, version, name string) (appupdate.Bundle, error) {
	if !appupdate.Within(job, bundle.Archive) {
		return bundle, errors.New("更新包不在当前交接目录")
	}
	manifest := mygo.UpdateManifest{Version: version, Size: bundle.Size, Signature: bundle.Signature}
	destination := filepath.Join(job, name)
	if err := mygo.ExtractVerifiedUpdateArchive(bundle.Archive, buildinfo.UpdatePublicKey, manifest, destination); err != nil {
		return bundle, err
	}
	metadata, err := appupdate.ReadBundle(destination, installation, version)
	if err != nil {
		return bundle, err
	}
	bundle.Directory, bundle.Metadata = destination, metadata
	return bundle, nil
}

func Acknowledge(home, path, token string) error {
	if token == "" || path == "" {
		return nil
	}
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 16 || filepath.Base(path) != "healthy" || !appupdate.Within(Cache(home), path) {
		return errors.New("无效的更新启动确认")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = file.WriteString(token)
	if err == nil {
		err = file.Sync()
	}
	if e := file.Close(); err == nil {
		err = e
	}
	return err
}
