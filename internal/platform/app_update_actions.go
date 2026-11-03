package platform

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"silverwing-go/internal/appupdate"
	"silverwing-go/internal/appupdate/native"
	"silverwing-go/internal/buildinfo"
	"silverwing-go/internal/domain"
)

func (s *Service) updateCoordinator() *appupdate.Coordinator {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if s.updates == nil {
		s.updates = appupdate.New(appUpdateSource{publicKey: buildinfo.UpdatePublicKey, newClient: func() (*http.Client, error) { return s.appUpdateHTTPClient(0) }}, native.Cache(s.paths.Home))
	}
	return s.updates
}

func (s *Service) updateInstallation() (appupdate.Installation, error) {
	executable, err := os.Executable()
	if err != nil {
		return appupdate.Installation{}, err
	}
	return native.Detect(executable, s.paths.Home, buildinfo.UpdatePublicKey, native.Identity())
}

func (s *Service) checkApplicationUpdate(ctx context.Context) (domain.UpdateCheckResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.UpdateCheckResult{}, err
	}
	client, route, err := s.appUpdateHTTPClientSnapshot(20 * time.Second)
	if err != nil {
		return domain.UpdateCheckResult{}, err
	}
	defer client.CloseIdleConnections()
	if err := s.updateBackoff.pending(route, time.Now()); err != nil {
		return domain.UpdateCheckResult{}, err
	}
	checker := s.updateChecker
	if checker == nil {
		checker = newGithubReleaseChecker()
	}
	installation, reason := s.updateInstallation()
	copy := *checker
	copy.client = client
	if reason == nil {
		copy.installationKind = installation.Kind
	} else if executable, err := os.Executable(); err == nil {
		copy.installationKind = native.DownloadKind(executable)
	}
	result, err := copy.check(ctx)
	s.updateBackoff.record(route, err)
	if reason == nil {
		result.InstallationKind = string(installation.Kind)
		result.CanAutoUpdate = result.Release != nil && result.Release.UpdateManifest != nil
		if !result.CanAutoUpdate {
			result.AutoUpdateReason = "该版本尚未提供签名更新包，可手动下载安装"
		}
	} else {
		result.AutoUpdateReason = reason.Error()
	}
	return result, err
}

func (s *Service) executeAppUpdate(ctx context.Context, kind string, payload map[string]any) (any, error) {
	coordinator := s.updateCoordinator()
	if kind == "update.status" {
		return coordinator.Status(), nil
	}
	if kind == "update.cancel" {
		return coordinator.CancelJob(payloadString(payload, "jobId"))
	}
	if err := requireConfirmed(payload, "应用更新"); err != nil {
		return nil, err
	}
	installation, err := s.updateInstallation()
	if err != nil {
		return nil, err
	}
	if kind == "update.prepare" {
		return coordinator.Prepare(ctx, installation, payloadString(payload, "version"))
	}
	plan, err := coordinator.Plan(payloadString(payload, "jobId"))
	if err != nil {
		return nil, err
	}
	if plan.Installation.Identity != installation.Identity || plan.Installation.Root != installation.Root || plan.Installation.Kind != installation.Kind || plan.Installation.Home != s.paths.Home {
		return nil, errors.New("更新交接的程序身份已变化")
	}
	job := filepath.Dir(plan.New.Archive)
	if filepath.Base(job) != "new" {
		return nil, errors.New("更新缓存无效")
	}
	job = filepath.Dir(job)
	if filepath.Dir(job) != native.Cache(s.paths.Home) {
		return nil, errors.New("更新缓存已越界")
	}
	helper := filepath.Join(job, filepath.Base(installation.Helper))
	if err := appupdate.CopyFile(installation.Helper, helper, 0o700); err != nil {
		return nil, err
	}
	if actual, e := appupdate.Digest(helper); e != nil {
		return nil, e
	} else if expected, e := appupdate.Digest(installation.Helper); e != nil || actual != expected {
		return nil, errors.New("更新助手校验失败")
	}
	if err := native.LaunchHelper(helper, filepath.Join(job, "plan.json")); err != nil {
		return nil, err
	}
	return coordinator.Status(), nil
}
