package platform

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/egoist/mygo"
	"silverwing-go/internal/appupdate"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/networkproxy"
)

type appUpdateSource struct {
	publicKey string
	client    *http.Client
	newClient func() (*http.Client, error)
}

type artifactSource struct {
	manifest, archive string
	client            *http.Client
}

func (s artifactSource) Manifest(ctx context.Context, _ bool) (io.ReadCloser, error) {
	return s.open(ctx, s.manifest)
}
func (s artifactSource) Open(ctx context.Context, address string) (io.ReadCloser, error) {
	if address != s.archive {
		return nil, fmt.Errorf("更新包地址与当前发行版本不一致")
	}
	return s.open(ctx, address)
}
func (s artifactSource) open(ctx context.Context, address string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Silverwing-Updater")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("读取签名更新包失败（HTTP %d）；可使用手动下载", response.StatusCode)
	}
	return response.Body, nil
}

func (source appUpdateSource) Prepare(ctx context.Context, installation appupdate.Installation, version, destination string, progress appupdate.Progress) (appupdate.Bundle, error) {
	target := appupdate.Target(installation.Identity.Platform, installation.Kind)
	base := "https://github.com/" + installation.Identity.Repository + "/releases/download/" + url.PathEscape(installation.Identity.TagPrefix+version) + "/"
	archiveName := installation.Identity.Executable + "-" + version + "-" + target + ".tar.gz"
	client := source.client
	var err error
	if client == nil {
		if source.newClient != nil {
			client, err = source.newClient()
		} else {
			client, err = networkproxy.NewHTTPClient(domain.AppConfig{}, domain.ProxyTargetAppUpdates, 0)
		}
		if err != nil {
			return appupdate.Bundle{}, err
		}
	}
	defer client.CloseIdleConnections()
	prepared, err := mygo.PrepareUpdate(ctx, artifactSource{manifest: base + "update-" + target + ".json", archive: base + archiveName, client: client}, source.publicKey, destination, progress)
	if err != nil {
		return appupdate.Bundle{}, err
	}
	if prepared.Manifest.Version != version {
		return appupdate.Bundle{}, fmt.Errorf("更新清单版本与当前选择不一致")
	}
	metadata, err := appupdate.ReadBundle(prepared.Contents, installation, version)
	if err != nil {
		return appupdate.Bundle{}, err
	}
	return appupdate.Bundle{Directory: prepared.Contents, Archive: prepared.Archive, Size: prepared.Manifest.Size, Signature: prepared.Manifest.Signature, Metadata: metadata}, nil
}
