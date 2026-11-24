package application

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/networkproxy"
	"silverwing-go/internal/snowball/auth"
	"silverwing-go/internal/snowball/dragonapi"
	"silverwing-go/internal/snowball/genbuapi"
	"silverwing-go/internal/storage"
)

type PlatformConnections struct {
	Username      string `json:"username"`
	Configured    bool   `json:"configured"`
	GenbuEnabled  bool   `json:"genbuEnabled"`
	DragonEnabled bool   `json:"dragonEnabled"`
	Revision      uint64 `json:"revision"`
	AccountError  string `json:"accountError,omitempty"`
}

type PlatformConnectionBackend interface {
	PlatformConnections(context.Context) (PlatformConnections, error)
}

// PlatformPasswordBackend supplies the saved password to the in-process settings editor.
type PlatformPasswordBackend interface {
	PlatformPassword(ctx context.Context, expectedRevision uint64) (string, error)
}

type embeddedPlatformState struct {
	enabled    bool
	generation uint64
}

type platformClients struct {
	mu          sync.Mutex
	fingerprint string
	revision    uint64
	genbuState  embeddedPlatformState
	dragonState embeddedPlatformState
	genbu       *genbuapi.Client
	dragon      *dragonapi.Client
	transport   http.RoundTripper // test transport, scoped to this application
}

func nativePlatform(tool string) bool { return tool == "genbu" || tool == "dragon" }

func (p *platformClients) state(tool string) *embeddedPlatformState {
	if tool == "genbu" {
		return &p.genbuState
	}
	return &p.dragonState
}

func (p *platformClients) retire(tool string) {
	p.state(tool).generation++
	if tool == "genbu" {
		p.genbu = nil
	} else {
		p.dragon = nil
	}
}

func loadPlatformAccount() (auth.Credentials, error) {
	store, err := auth.UserStore()
	if err != nil {
		return auth.Credentials{}, err
	}
	return store.Load()
}

// refreshPlatformConnections is called with the client registry locked. Each
// platform has its own generation so closing one cannot retire the other.
func (a *App) refreshPlatformConnections(cfg domain.AppConfig) (PlatformConnections, auth.Credentials) {
	p := &a.connections
	account, err := loadPlatformAccount()
	configured := err == nil
	fingerprint := ""
	if configured {
		fingerprint = account.Fingerprint()
	}
	changed := fingerprint != p.fingerprint
	if changed {
		p.fingerprint = fingerprint
		p.revision++
	}
	for _, tool := range []string{"genbu", "dragon"} {
		state := p.state(tool)
		enabled := cfg.PlatformEnabled(tool, configured)
		if changed || enabled != state.enabled {
			state.enabled = enabled
			p.retire(tool)
		}
	}
	result := PlatformConnections{Username: account.Username, Configured: configured,
		GenbuEnabled: p.genbuState.enabled, DragonEnabled: p.dragonState.enabled, Revision: p.revision}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		result.AccountError = err.Error()
	}
	return result, account
}

func (a *App) PlatformConnections(ctx context.Context) (PlatformConnections, error) {
	a.connections.mu.Lock()
	defer a.connections.mu.Unlock()
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return PlatformConnections{}, err
	}
	status, _ := a.refreshPlatformConnections(cfg)
	return status, nil
}

func (a *App) PlatformPassword(ctx context.Context, expectedRevision uint64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	a.connections.mu.Lock()
	defer a.connections.mu.Unlock()
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return "", err
	}
	status, account := a.refreshPlatformConnections(cfg)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !status.Configured || status.Revision != expectedRevision {
		return "", errPlatformScopeChanged
	}
	return account.Password, nil
}

func (a *App) platformReadyFor(cfg domain.AppConfig, tool string) bool {
	// Task and UI readers sometimes hold older configuration snapshots.
	// Those snapshots must never advance the live connection generation.
	_, err := loadPlatformAccount()
	return err == nil && cfg.PlatformEnabled(tool, true)
}

func (a *App) embeddedPlatformStatus(ctx context.Context, tool string) (OptionalCLIStatus, error) {
	result := OptionalCLIStatus{Tool: tool, Phase: OptionalCLIUnavailable}
	status, err := a.PlatformConnections(ctx)
	if err != nil {
		return result, err
	}
	enabled := status.GenbuEnabled
	if tool == "dragon" {
		enabled = status.DragonEnabled
	}
	if !enabled {
		result.Phase = OptionalCLIDisabled
	} else if status.Configured {
		result.Phase = OptionalCLIAvailable
	} else {
		result.Error = platformUnavailable(status, tool).Error()
	}
	return result, nil
}

func platformUnavailable(status PlatformConnections, tool string) error {
	if status.AccountError != "" {
		return errors.New(status.AccountError)
	}
	if !status.Configured {
		return fmt.Errorf("%s 尚未配置共用账号，请在「工具与连接 → 平台连接」中保存", optionalCLIName(tool))
	}
	return fmt.Errorf("%s 已关闭，请在「工具与连接 → 平台连接」中启用", optionalCLIName(tool))
}

var errPlatformScopeChanged = errors.New("平台连接或共用账号已变化，请重新打开操作")

func (a *App) platformAdmission(ctx context.Context, tool string) (func(context.Context) error, error) {
	if a.optionalCLIRequire != nil {
		// Business fixtures inject the availability of dependencies they stub.
		if err := a.optionalCLIRequire(ctx, tool); err != nil {
			return nil, err
		}
		return func(ctx context.Context) error { return a.optionalCLIRequire(ctx, tool) }, nil
	}
	a.connections.mu.Lock()
	defer a.connections.mu.Unlock()
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return nil, err
	}
	status, _ := a.refreshPlatformConnections(cfg)
	state := *a.connections.state(tool)
	if !status.Configured || !state.enabled {
		return nil, platformUnavailable(status, tool)
	}
	return func(ctx context.Context) error { return a.checkPlatformGeneration(ctx, tool, state.generation) }, nil
}

func (a *App) checkPlatformGeneration(ctx context.Context, tool string, generation uint64) error {
	a.connections.mu.Lock()
	defer a.connections.mu.Unlock()
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return err
	}
	status, _ := a.refreshPlatformConnections(cfg)
	state := a.connections.state(tool)
	if !status.Configured || !state.enabled || state.generation != generation {
		return errPlatformScopeChanged
	}
	return ctx.Err()
}

func (a *App) platformIntegrationContext(ctx context.Context, _ domain.AppConfig, tool string) (context.Context, error) {
	check, err := a.platformAdmission(ctx, tool)
	if err != nil {
		return ctx, err
	}
	return commandruntime.WithAdmission(ctx, check), nil
}

type embeddedTransport struct {
	base  http.RoundTripper
	check func(context.Context) error
}

func (t embeddedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := commandruntime.CheckAdmission(req.Context()); err != nil {
		return nil, err
	}
	if err := commandruntime.CheckAdmission(commandruntime.WithAdmission(req.Context(), t.check)); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

func (a *App) platformHTTP(cfg domain.AppConfig, tool string, timeout time.Duration) (*http.Client, error) {
	client, err := networkproxy.NewHTTPClient(cfg, "", timeout)
	if err != nil {
		return nil, err
	}
	if a.connections.transport != nil {
		client.Transport = a.connections.transport
	}
	generation := a.connections.state(tool).generation
	client.Transport = embeddedTransport{base: client.Transport, check: func(ctx context.Context) error {
		return a.checkPlatformGeneration(ctx, tool, generation)
	}}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client, nil
}

func (a *App) embeddedGenbu(ctx context.Context, _ domain.AppConfig) (*genbuapi.Client, error) {
	p := &a.connections
	p.mu.Lock()
	defer p.mu.Unlock()
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return nil, err
	}
	status, account := a.refreshPlatformConnections(cfg)
	if !status.Configured || !status.GenbuEnabled {
		return nil, platformUnavailable(status, "genbu")
	}
	if p.genbu == nil {
		httpClient, err := a.platformHTTP(cfg, "genbu", genbuapi.DefaultTimeout)
		if err != nil {
			return nil, err
		}
		p.genbu, err = genbuapi.NewClient(genbuapi.Config{BeastURL: genbuapi.DefaultBeastURL, GenbuURL: genbuapi.DefaultGenbuURL,
			Username: account.Username, Password: account.Password, Timeout: genbuapi.DefaultTimeout, HTTPClient: httpClient})
		if err != nil {
			return nil, err
		}
	}
	return p.genbu, nil
}

func (a *App) embeddedDragon(ctx context.Context, _ domain.AppConfig) (*dragonapi.Client, error) {
	p := &a.connections
	p.mu.Lock()
	defer p.mu.Unlock()
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return nil, err
	}
	status, account := a.refreshPlatformConnections(cfg)
	if !status.Configured || !status.DragonEnabled {
		return nil, platformUnavailable(status, "dragon")
	}
	if p.dragon == nil {
		httpClient, err := a.platformHTTP(cfg, "dragon", dragonapi.DefaultTimeout)
		if err != nil {
			return nil, err
		}
		p.dragon, err = dragonapi.NewClient(dragonapi.Config{BeastURL: dragonapi.DefaultBeastURL, DragonURL: dragonapi.DefaultDragonURL,
			Username: account.Username, Password: account.Password, Timeout: dragonapi.DefaultTimeout, HTTPClient: httpClient,
			CheckAdmission: commandruntime.CheckAdmission})
		if err != nil {
			return nil, err
		}
	}
	return p.dragon, nil
}

func (a *App) embeddedAccountIdentity(ctx context.Context, tool string) (string, error) {
	if err := commandruntime.CheckAdmission(ctx); err != nil {
		return "", err
	}
	a.connections.mu.Lock()
	defer a.connections.mu.Unlock()
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return "", err
	}
	status, account := a.refreshPlatformConnections(cfg)
	state := a.connections.state(tool)
	if !status.Configured || !state.enabled {
		return "", platformUnavailable(status, tool)
	}
	if tool == "dragon" {
		return account.Username, nil
	}
	return fmt.Sprintf("%s:%d:%s", a.Paths.ProfileID, state.generation, account.Fingerprint()), nil
}

func (a *App) platformConnectionAction(ctx context.Context, action Action) (ActionResult, error) {
	tool := action.ServiceID
	if action.Kind != "platform.connections" && action.Kind != "platform.credentials.save" && !nativePlatform(tool) {
		return ActionResult{}, errors.New("请选择 Genbu 或 Dragon")
	}
	var message string
	switch action.Kind {
	case "platform.credentials.save":
		if err := a.savePlatformCredentials(ctx, action); err != nil {
			return ActionResult{}, err
		}
		message = "共用账号已保存"
	case "platform.connection.set":
		enabled, ok := action.Payload["enabled"].(bool)
		if !ok {
			return ActionResult{}, errors.New("平台开关值无效")
		}
		a.connections.mu.Lock()
		cfg, err := a.Config.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
			if tool == "genbu" {
				cfg.OptionalCLIs.Genbu = &enabled
			} else {
				cfg.OptionalCLIs.Dragon = &enabled
			}
			return cfg, ctx.Err()
		})
		if err == nil {
			a.refreshPlatformConnections(cfg)
		}
		a.connections.mu.Unlock()
		if err != nil {
			return ActionResult{}, err
		}
	case "platform.login.test":
		if err := a.testPlatformLogin(ctx, tool); err != nil {
			return ActionResult{}, err
		}
		message = optionalCLIName(tool) + " 登录成功"
	}
	status, err := a.PlatformConnections(ctx)
	return ActionResult{Data: status, Message: message}, err
}

func (a *App) savePlatformCredentials(ctx context.Context, action Action) error {
	a.connections.mu.Lock()
	defer a.connections.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	store, err := auth.UserStore()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(store.Directory, 0o700); err != nil {
		return errors.New("无法创建共享账号目录")
	}
	release, err := storage.AcquireLock(filepath.Join(store.Directory, "silverwing-credentials.lock"))
	if err != nil {
		return err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return err
	}
	username := strings.TrimSpace(textPayload(action, "username"))
	password, _ := action.Payload["password"].(string)
	previous, _ := store.Load()
	if password == "" && username == previous.Username {
		password = previous.Password
	}
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return err
	}
	if _, err := store.Save(auth.Credentials{Username: username, Password: password}); err != nil {
		return err
	}
	a.refreshPlatformConnections(cfg)
	return nil
}

func (a *App) testPlatformLogin(ctx context.Context, tool string) error {
	ctx, err := a.platformIntegrationContext(ctx, domain.AppConfig{}, tool)
	if err != nil {
		return err
	}
	if tool == "genbu" {
		client, acquireErr := a.embeddedGenbu(ctx, domain.AppConfig{})
		if acquireErr != nil {
			return acquireErr
		}
		_, err = client.Login(ctx)
	} else {
		client, acquireErr := a.embeddedDragon(ctx, domain.AppConfig{})
		if acquireErr != nil {
			return acquireErr
		}
		_, err = client.Login(ctx)
	}
	if err != nil {
		if check := commandruntime.CheckAdmission(ctx); check != nil {
			return check
		}
		return fmt.Errorf("%s 登录失败，请检查账号密码与网络连接", optionalCLIName(tool))
	}
	return commandruntime.CheckAdmission(ctx)
}
