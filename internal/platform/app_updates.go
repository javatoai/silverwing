package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"silverwing-go/internal/appupdate"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/networkproxy"
	"silverwing-go/internal/productprofile"
)

type githubReleaseChecker struct {
	client                         *http.Client
	apiBase, repository, tagPrefix string
	executable, goos, goarch       string
	currentVersion                 string
	installationKind               appupdate.Kind
}

type githubReleaseRecord struct {
	Tag         string `json:"tag_name"`
	URL         string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	Assets      []struct {
		Name  string `json:"name"`
		URL   string `json:"browser_download_url"`
		State string `json:"state"`
		Size  int64  `json:"size"`
	} `json:"assets"`
}

func newGithubReleaseChecker() *githubReleaseChecker {
	client, _ := networkproxy.NewHTTPClient(domain.AppConfig{}, domain.ProxyTargetAppUpdates, 20*time.Second)
	return &githubReleaseChecker{
		client: client, apiBase: "https://api.github.com",
		repository: productprofile.ReleaseRepository, tagPrefix: productprofile.ReleaseTagPrefix,
		executable: productprofile.ExecutableName, goos: runtime.GOOS, goarch: runtime.GOARCH,
		currentVersion: domain.CurrentProductVersion,
	}
}

func (checker *githubReleaseChecker) check(ctx context.Context) (domain.UpdateCheckResult, error) {
	result := domain.UpdateCheckResult{CurrentVersion: checker.currentVersion,
		Repository: checker.repository, TagPrefix: checker.tagPrefix,
		Platform:    checker.goos + "/" + checker.goarch,
		ReleasesURL: productprofile.ReleasesURL()}
	base := checker.apiBase + "/repos/" + checker.repository + "/releases"
	var latest githubReleaseRecord
	if checker.tagPrefix == "v" {
		if err := checker.get(ctx, base+"/latest", &latest); err != nil {
			return result, err
		}
		release, err := checker.release(latest)
		if err != nil {
			return result, err
		}
		if release != nil {
			result.Release = release
			result.CheckedAt = time.Now().Format(time.RFC3339)
			return result, nil
		}
	}
	// Edition releases share a repository, but must never offer another edition.
	// Search a bounded set and fail explicitly if it cannot establish the channel.
	complete := false
	for page := 1; page <= 5; page++ {
		var records []githubReleaseRecord
		if err := checker.get(ctx, fmt.Sprintf("%s?per_page=100&page=%d", base, page), &records); err != nil {
			return result, err
		}
		for _, record := range records {
			release, err := checker.release(record)
			if err != nil {
				return result, err
			}
			if release == nil {
				continue
			}
			if result.Release == nil {
				result.Release = release
			} else if compare, valid := domain.CompareReleaseVersions(release.Version, result.Release.Version); valid && compare > 0 {
				result.Release = release
			}
		}
		if len(records) < 100 {
			complete = true
			break
		}
	}
	if !complete {
		return result, errors.New("版本列表未能完整读取，请打开 GitHub Releases 查看")
	}
	result.CheckedAt = time.Now().Format(time.RFC3339)
	return result, nil
}

func (checker *githubReleaseChecker) release(record githubReleaseRecord) (*domain.UpdateRelease, error) {
	if record.Draft || record.Prerelease || !strings.HasPrefix(record.Tag, checker.tagPrefix) {
		return nil, nil
	}
	version := strings.TrimPrefix(record.Tag, checker.tagPrefix)
	if _, valid := domain.CompareReleaseVersions(version, version); !valid {
		return nil, nil
	}
	release := &domain.UpdateRelease{Version: version, Tag: record.Tag, URL: record.URL, PublishedAt: record.PublishedAt}
	for _, name := range checker.packageNames(version) {
		for _, asset := range record.Assets {
			if asset.Name == name && asset.State == "uploaded" && asset.Size > 0 {
				release.Package = &domain.UpdateAsset{Name: asset.Name, URL: asset.URL, Size: asset.Size}
				break
			}
		}
		if release.Package != nil {
			break
		}
	}
	result := domain.UpdateCheckResult{Repository: checker.repository, TagPrefix: checker.tagPrefix, Release: release}
	if checker.installationKind != "" {
		name := "update-" + appupdate.Target(checker.goos+"/"+checker.goarch, checker.installationKind) + ".json"
		for _, asset := range record.Assets {
			if asset.Name == name && asset.State == "uploaded" && asset.Size > 0 {
				release.UpdateManifest = &domain.UpdateAsset{Name: asset.Name, URL: asset.URL, Size: asset.Size}
				break
			}
		}
	}
	if !result.Valid(checker.repository, checker.tagPrefix) {
		return nil, errors.New("GitHub 版本信息不完整或下载地址无法识别，请打开发布页面查看")
	}
	return release, nil
}

func (checker *githubReleaseChecker) packageNames(version string) []string {
	base := checker.executable + "-" + version + "-"
	switch {
	case checker.goos == "windows" && checker.goarch == "amd64":
		if checker.installationKind == "manual" {
			return nil
		}
		if checker.installationKind == appupdate.MSI {
			return []string{base + "windows-x64.msi"}
		}
		if checker.installationKind == appupdate.Portable {
			return []string{base + "windows-x64-portable.zip"}
		}
		return []string{base + "windows-x64-setup.exe", base + "windows-x64.msi", base + "windows-x64-portable.zip"}
	case checker.goos == "darwin" && checker.goarch == "arm64":
		return []string{base + "macos-arm64.dmg"}
	default:
		return nil
	}
}

func (checker *githubReleaseChecker) get(ctx context.Context, address string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	request.Header.Set("User-Agent", checker.executable+"/"+checker.currentVersion)
	response, err := checker.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("连接 GitHub 失败，请检查网络后重试：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		return githubReleaseResponseError(response, body, time.Now())
	}
	const maxBody = 4 << 20
	content, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("读取 GitHub 响应失败：%w", err)
	}
	if len(content) > maxBody {
		return errors.New("GitHub 版本响应过大，请打开发布页面查看")
	}
	if strings.TrimSpace(string(content)) == "null" {
		return errors.New("GitHub 版本响应为空，请稍后重试")
	}
	if err := json.Unmarshal(content, destination); err != nil {
		return errors.New("GitHub 版本响应无法识别，请稍后重试")
	}
	return nil
}
