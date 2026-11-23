package desktop

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/asyncjob"
)

const platformConnectionsPage = "platform-connections"

type desktopPlatformConnections struct {
	home, profile     string
	value             application.PlatformConnections
	loaded            bool
	attempted         bool
	read              *asyncjob.Lease
	save              *asyncjob.Lease
	readError         string
	saveMessage       string
	saveFailed        bool
	username          string
	password          string
	passwordRead      *asyncjob.Lease
	passwordReadError string
	passwordRequested bool
	passwordEdited    bool
	passwordVisible   bool
	genbu, dragon     desktopPlatformConnection
}

type desktopPlatformConnection struct {
	ctx           context.Context
	cancel        context.CancelFunc
	test          *asyncjob.Lease
	testAttempted bool
	testMessage   string
	testFailed    bool
	draftEnabled  bool
}

func (state *desktopPlatformConnections) connection(tool string) *desktopPlatformConnection {
	if tool == "genbu" {
		return &state.genbu
	}
	return &state.dragon
}

func (state *desktopPlatformConnections) enabled(tool string) bool {
	if tool == "genbu" {
		return state.value.GenbuEnabled
	}
	return state.value.DragonEnabled
}

func (s *app) integrationReady(tool string) bool {
	if desktopHasOptionalCLI(tool) {
		return s.optionalCLIReady(tool)
	}
	state := s.settingsUnit.state.platformConnections
	return (tool == "genbu" || tool == "dragon") && state != nil && state.loaded &&
		state.value.Configured && state.enabled(tool)
}

func (s *app) integrationContext(tool string) context.Context {
	if desktopHasOptionalCLI(tool) {
		return s.optionalCLIContext(tool)
	}
	if !s.integrationReady(tool) {
		return inactiveOptionalCLIContext
	}
	connection := s.settingsUnit.state.platformConnections.connection(tool)
	if connection.ctx == nil {
		connection.ctx, connection.cancel = context.WithCancel(context.Background())
	}
	return connection.ctx
}

func (s *app) ensurePlatformConnections() *desktopPlatformConnections {
	state := s.settingsUnit.state.platformConnections
	if state != nil && state.home == s.snapshot.Home && state.profile == s.snapshot.ProfileID {
		return state
	}
	next := &desktopPlatformConnections{home: s.snapshot.Home, profile: s.snapshot.ProfileID}
	s.settingsUnit.state.platformConnections = next
	if state != nil {
		if state.read != nil {
			state.read.Cancel()
		}
		state.resetPlatformPassword()
		for _, tool := range [...]string{"genbu", "dragon"} {
			s.retirePlatformConnection(state, tool)
		}
	}
	return next
}

func (s *app) retirePlatformConnection(state *desktopPlatformConnections, tool string) {
	connection := state.connection(tool)
	if connection.cancel != nil {
		connection.cancel()
	}
	if connection.test != nil {
		connection.test.Cancel()
	}
	*connection = desktopPlatformConnection{}
	s.retireIntegrationViews(tool)
}

func (s *app) publishPlatformConnections(state *desktopPlatformConnections, next application.PlatformConnections) {
	before, wasLoaded := state.value, state.loaded
	state.value, state.loaded = next, true
	credentialsChanged := before.Revision != next.Revision || before.Configured != next.Configured || before.Username != next.Username
	if wasLoaded && credentialsChanged {
		state.resetPlatformPassword()
	}
	if !wasLoaded || state.username == before.Username {
		state.username = next.Username
	}
	if wasLoaded {
		if credentialsChanged || before.GenbuEnabled != next.GenbuEnabled {
			s.retirePlatformConnection(state, "genbu")
		}
		if credentialsChanged || before.DragonEnabled != next.DragonEnabled {
			s.retirePlatformConnection(state, "dragon")
		}
	}
	s.resolveInitialGenbuRoute()
	s.reconcileSidebarNavigation()
	s.reconcileTaskContentTabs()
	if wasLoaded && credentialsChanged && s.page == "production" && s.productionAvailable() {
		s.enterProductionPage()
	}
}

// Configuration import and local refresh can change the persisted switches.
// Drop affected scopes immediately while the local metadata read catches up.
func (s *app) syncPlatformConnections(previousConfig string) {
	if _, ok := s.backend.(application.PlatformConnectionBackend); !ok {
		return
	}
	state := s.ensurePlatformConnections()
	current, err := s.readCurrentAppConfig()
	before, previousErr := decodeAppConfig(previousConfig)
	switchesChanged := previousErr != nil || err != nil
	if err == nil && previousErr == nil {
		configured := state.value.Configured
		switchesChanged = before.PlatformEnabled("genbu", configured) != current.PlatformEnabled("genbu", configured) ||
			before.PlatformEnabled("dragon", configured) != current.PlatformEnabled("dragon", configured)
		if switchesChanged && state.loaded {
			next := state.value
			next.GenbuEnabled, next.DragonEnabled = current.PlatformEnabled("genbu", configured), current.PlatformEnabled("dragon", configured)
			s.publishPlatformConnections(state, next)
		}
	}
	s.requestPlatformConnections(switchesChanged)
}

func (s *app) requestPlatformConnections(force bool) {
	state := s.ensurePlatformConnections()
	backend, ok := s.backend.(application.PlatformConnectionBackend)
	if !ok || state.save != nil || (!force && state.attempted) {
		return
	}
	if state.read != nil {
		state.read.Cancel()
	}
	lease := s.beginPlatformRequest(false)
	if lease == nil {
		return
	}
	state.read, state.attempted, state.readError = lease, true, ""
	s.runPlatformRequest(lease, func(ctx context.Context) (application.ActionResult, error) {
		value, err := backend.PlatformConnections(ctx)
		return application.ActionResult{Data: value}, err
	}, func(result application.ActionResult, err error) {
		if s.settingsUnit.state.platformConnections != state || state.read != lease {
			return
		}
		state.read = nil
		if err != nil {
			state.readError = "读取平台连接设置失败。"
			return
		}
		s.publishPlatformConnections(state, result.Data.(application.PlatformConnections))
	})
}

func (s *app) platformAccountPageActive() bool {
	return !s.loading && s.page == "settings" && s.settingsUnit.state.settingsPage == platformConnectionsPage
}

func (state *desktopPlatformConnections) resetPlatformPassword() {
	if state.passwordRead != nil {
		state.passwordRead.Cancel()
		state.passwordRead = nil
	}
	state.password, state.passwordReadError = "", ""
	state.passwordRequested, state.passwordEdited, state.passwordVisible = false, false, false
}

func (state *desktopPlatformConnections) leavePlatformAccountPage() {
	state.resetPlatformPassword()
	for _, connection := range [...]*desktopPlatformConnection{&state.genbu, &state.dragon} {
		if connection.test != nil {
			connection.test.Cancel()
			connection.test = nil
		}
		connection.testAttempted = false
	}
}

func (s *app) requestPlatformPassword(state *desktopPlatformConnections) {
	backend, ok := s.backend.(application.PlatformPasswordBackend)
	if !ok || !s.platformAccountPageActive() || !state.value.Configured || state.read != nil || state.save != nil ||
		state.passwordRequested || state.passwordEdited || strings.TrimSpace(state.username) != state.value.Username {
		return
	}
	lease := s.beginPlatformRequest(false)
	if lease == nil {
		return
	}
	revision := state.value.Revision
	state.passwordRead, state.passwordRequested = lease, true
	s.runPlatformRequest(lease, func(ctx context.Context) (application.ActionResult, error) {
		password, err := backend.PlatformPassword(ctx, revision)
		return application.ActionResult{Data: password}, err
	}, func(result application.ActionResult, err error) {
		if s.settingsUnit.state.platformConnections != state || state.passwordRead != lease {
			return
		}
		state.passwordRead = nil
		s.applyPlatformPassword(state, revision, result.Data.(string), err)
	})
}

func (s *app) applyPlatformPassword(state *desktopPlatformConnections, revision uint64, password string, err error) {
	if !s.platformAccountPageActive() || state.value.Revision != revision || state.passwordEdited ||
		strings.TrimSpace(state.username) != state.value.Username {
		return
	}
	if err != nil {
		state.passwordReadError = "读取已保存的密码失败。"
		return
	}
	state.password = password
}

func (s *app) savePlatformConnection(state *desktopPlatformConnections, action application.Action) {
	if !state.loaded || state.save != nil {
		return
	}
	lease := s.beginPlatformRequest(true)
	if lease == nil {
		return
	}
	if state.read != nil {
		state.read.Cancel()
		state.read = nil
	}
	state.save, state.saveMessage, state.saveFailed = lease, "", false
	backend := s.backend
	kind, tool := action.Kind, action.ServiceID
	s.runPlatformRequest(lease, func(ctx context.Context) (application.ActionResult, error) {
		defer delete(action.Payload, "password")
		return backend.Dispatch(ctx, action)
	}, func(result application.ActionResult, err error) {
		if s.settingsUnit.state.platformConnections != state || state.save != lease {
			return
		}
		state.save = nil
		next, ok := result.Data.(application.PlatformConnections)
		if err != nil || !ok {
			state.saveMessage, state.saveFailed = "保存失败，请核对账号或稍后重试。", true
			s.requestPlatformConnections(true)
			return
		}
		if kind == "platform.credentials.save" {
			state.username = next.Username
			state.resetPlatformPassword()
		} else {
			s.mergeSavedPlatformSwitch(tool, next)
		}
		state.saveMessage = "已保存"
		s.publishPlatformConnections(state, next)
	})
}

func (state *desktopPlatformConnections) close() {
	state.leavePlatformAccountPage()
	for _, connection := range [...]*desktopPlatformConnection{&state.genbu, &state.dragon} {
		if connection.cancel != nil {
			connection.cancel()
		}
	}
}

func (s *app) mergeSavedPlatformSwitch(tool string, next application.PlatformConnections) {
	enabled := next.DragonEnabled
	if tool == "genbu" {
		enabled = next.GenbuEnabled
	}
	cfg, err := s.settingsConfigMap()
	if err != nil {
		return
	}
	switches, _ := cfg["optionalCLIs"].(map[string]any)
	if switches == nil {
		switches = make(map[string]any)
		cfg["optionalCLIs"] = switches
	}
	switches[tool] = enabled
	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err == nil {
		value := string(encoded)
		s.syncSettingsConfigInputs(s.settingsUnit.state.configJSON, value)
		s.settingsUnit.state.configJSON, s.snapshot.ConfigJSON = value, value
	}
}

func (s *app) observePlatformLoginTests(state *desktopPlatformConnections) {
	if !s.platformAccountPageActive() || !s.pageLocalReadReady() || state.read != nil || state.readError != "" {
		return
	}
	for _, tool := range [...]string{"genbu", "dragon"} {
		if !state.connection(tool).testAttempted {
			s.testPlatformLogin(state, tool)
		}
	}
}

func (s *app) testPlatformLogin(state *desktopPlatformConnections, tool string) {
	connection := state.connection(tool)
	if !s.platformAccountPageActive() || s.settingsUnit.state.platformConnections != state ||
		state.read != nil || state.save != nil || connection.test != nil || !s.integrationReady(tool) {
		return
	}
	lease := s.beginPlatformRequest(false)
	if lease == nil {
		return
	}
	connection.test, connection.testAttempted = lease, true
	connection.testMessage, connection.testFailed = "", false
	backend := s.backend
	s.runPlatformRequest(lease, func(ctx context.Context) (application.ActionResult, error) {
		return backend.Dispatch(ctx, application.Action{Kind: "platform.login.test", ServiceID: tool})
	}, func(_ application.ActionResult, err error) {
		s.finishPlatformLogin(state, tool, lease, err)
	})
}

func (s *app) finishPlatformLogin(state *desktopPlatformConnections, tool string, lease *asyncjob.Lease, err error) {
	connection := state.connection(tool)
	if s.settingsUnit.state.platformConnections != state || connection.test != lease {
		return
	}
	connection.test = nil
	if !s.platformAccountPageActive() {
		return
	}
	connection.testFailed = err != nil
	connection.testMessage = "登录成功"
	if err != nil {
		connection.testMessage = "登录失败"
	}
}

func (s *app) beginPlatformRequest(write bool) *asyncjob.Lease {
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		return nil
	}
	lease, _ := s.settingsUnit.state.platformJobs.Begin(context.Background(), asyncjob.Options{CancelOnClose: !write})
	return lease
}

func (s *app) runPlatformRequest(lease *asyncjob.Lease, run func(context.Context) (application.ActionResult, error), finish func(application.ActionResult, error)) {
	win, jobs := s.win, &s.settingsUnit.state.platformJobs
	win.Invalidate()
	go func() {
		ctx, cancel := context.WithTimeout(lease.Context(), 30*time.Second)
		result, err := run(ctx)
		cancel()
		if lease.Context().Err() == nil {
			jobs.Post(func(callback func()) bool {
				if win.IsDestroyed() {
					return false
				}
				win.Update(callback)
				return true
			}, func() {
				if s.win == win && !win.IsDestroyed() {
					finish(result, err)
				}
			})
		}
		lease.Cancel()
		lease.Complete(nil)
	}()
}
