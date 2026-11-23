package production

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/snowball/genbuapi"
)

// GenbuConfigMaxBytes bounds the UTF-8 source and draft accepted by the editor.
const GenbuConfigMaxBytes = 4 << 20

type GenbuConfigSnapshot struct {
	Service     string `json:"service"`
	Environment string `json:"environment"`
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	UpdateTime  string `json:"update_time"`
	SnapshotID  string `json:"snapshot_id"`
}

type GenbuConfigFiles struct {
	GenbuConfigSnapshot
	Files []string `json:"files"`
}

type GenbuConfigFile struct {
	GenbuConfigSnapshot
	File    string `json:"file"`
	Content string `json:"-"`
}

func (g NativeGenbu) ListConfigFiles(ctx context.Context, cfg domain.AppConfig, env, service string) (GenbuConfigFiles, error) {
	wire, snapshot, err := g.queryConfig(ctx, cfg, env, service, "")
	if err != nil {
		return GenbuConfigFiles{}, err
	}
	if wire.Files == nil {
		return GenbuConfigFiles{}, errors.New("无法识别 Genbu 配置文件列表")
	}
	for _, file := range *wire.Files {
		if !validGenbuConfigIdentifier(file) {
			return GenbuConfigFiles{}, ErrGenbuConfigTarget
		}
	}
	return GenbuConfigFiles{GenbuConfigSnapshot: snapshot, Files: *wire.Files}, nil
}

func (g NativeGenbu) ReadConfigFile(ctx context.Context, cfg domain.AppConfig, env, service, file string) (GenbuConfigFile, error) {
	if !validGenbuConfigIdentifier(file) {
		return GenbuConfigFile{}, errors.New("请选择有效的 Genbu 目标文件名")
	}
	wire, snapshot, err := g.queryConfig(ctx, cfg, env, service, file)
	if err != nil {
		return GenbuConfigFile{}, err
	}
	if wire.Content == nil || wire.File != file || !utf8.ValidString(*wire.Content) {
		return GenbuConfigFile{}, errors.New("Genbu 返回的文件与所选目标不匹配，或缺少有效的文件正文")
	}
	if len(*wire.Content) > GenbuConfigMaxBytes {
		return GenbuConfigFile{}, errors.New("配置文件超过 4 MiB，无法内联编辑")
	}
	return GenbuConfigFile{GenbuConfigSnapshot: snapshot, File: wire.File, Content: *wire.Content}, nil
}

func (g NativeGenbu) queryConfig(ctx context.Context, cfg domain.AppConfig, env, service, file string) (genbuapi.ConfigSnapshotResult, GenbuConfigSnapshot, error) {
	env, err := serviceEnvironment(env)
	if err != nil {
		return genbuapi.ConfigSnapshotResult{}, GenbuConfigSnapshot{}, err
	}
	service = strings.TrimSpace(service)
	if !genbuName(service) {
		return genbuapi.ConfigSnapshotResult{}, GenbuConfigSnapshot{}, errors.New("请先配置有效的 Genbu 服务名")
	}
	ctx, client, err := g.client(ctx, cfg)
	if err != nil {
		return genbuapi.ConfigSnapshotResult{}, GenbuConfigSnapshot{}, err
	}
	wire, err := client.QueryConfigSnapshot(ctx, genbuapi.ConfigSnapshotRequest{Service: service, Environment: env, File: file})
	if scopeErr := checkGenbuReadScope(ctx); scopeErr != nil {
		return genbuapi.ConfigSnapshotResult{}, GenbuConfigSnapshot{}, scopeErr
	}
	if err != nil {
		return genbuapi.ConfigSnapshotResult{}, GenbuConfigSnapshot{}, genbuConfigError(ctx, err, "Genbu 配置查询失败")
	}
	snapshot := GenbuConfigSnapshot{
		Service: wire.Service, Environment: genbuResponseEnvironment(wire.Environment), Namespace: wire.Namespace,
		Name: wire.Name, UpdateTime: wire.UpdateTime, SnapshotID: wire.SnapshotID,
	}
	if snapshot.Environment != env || snapshot.Service != genbuapi.ResolveService(service).Name ||
		!validGenbuConfigIdentifier(snapshot.Namespace) || !validGenbuConfigIdentifier(snapshot.Name) ||
		!validGenbuConfigIdentifier(snapshot.UpdateTime) || !validGenbuSHA256(snapshot.SnapshotID) {
		return genbuapi.ConfigSnapshotResult{}, GenbuConfigSnapshot{}, ErrGenbuConfigTarget
	}
	return wire, snapshot, nil
}

func validGenbuConfigIdentifier(value string) bool {
	return genbuIdentifier(value) && strings.TrimSpace(value) != ""
}
