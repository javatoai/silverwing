package desktop

import (
	"context"
	"encoding/json"

	"silverwing-go/internal/application"
	"silverwing-go/internal/asyncjob"
)

type desktopOptionalCLIScope struct {
	home, path string
	enabled    bool
}

// These records and their scope contexts are owned by the window thread. A
// worker can publish a result only while its record is still current.
type desktopOptionalCLIState struct {
	scope  desktopOptionalCLIScope
	ctx    context.Context
	cancel context.CancelFunc
	status application.OptionalCLIStatus
}

var desktopOptionalCLITools = [...]string{"lark"}

func desktopHasOptionalCLI(tool string) bool {
	for _, optional := range desktopOptionalCLITools {
		if tool == optional {
			return true
		}
	}
	return false
}

func (s *app) optionalCLIScope(tool string) desktopOptionalCLIScope {
	config, err := s.readCurrentAppConfig()
	return desktopOptionalCLIScope{home: s.snapshot.Home, path: config.OptionalCLIPath(tool), enabled: err == nil && config.OptionalCLIEnabled(tool)}
}

func (s *app) currentOptionalCLI(tool string) *desktopOptionalCLIState {
	if jobs := s.toolsUnit.state.cliJobs; jobs != nil {
		if current := jobs.optional[tool]; current != nil && current.scope == s.optionalCLIScope(tool) {
			return current
		}
	}
	return nil
}

func (s *app) optionalCLIStatus(tool string) application.OptionalCLIStatus {
	if !s.optionalCLIScope(tool).enabled {
		return application.OptionalCLIStatus{Tool: tool, Phase: application.OptionalCLIDisabled}
	}
	if current := s.currentOptionalCLI(tool); current != nil {
		return current.status
	}
	if _, ok := s.backend.(application.OptionalCLIBackend); !ok {
		// Simple desktop backends can provide business operations without a
		// process runner. They still have to opt in through configuration.
		return application.OptionalCLIStatus{Tool: tool, Phase: application.OptionalCLIAvailable}
	}
	return application.OptionalCLIStatus{Tool: tool, Phase: application.OptionalCLIProbing}
}

func (s *app) optionalCLIReady(tool string) bool {
	return s.optionalCLIStatus(tool).Ready()
}

var inactiveOptionalCLIContext = func() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}()

func (s *app) optionalCLIContext(tool string) context.Context {
	if !s.optionalCLIReady(tool) {
		return inactiveOptionalCLIContext
	}
	if current := s.currentOptionalCLI(tool); current != nil {
		return current.ctx
	}
	return s.replaceOptionalCLI(tool, s.optionalCLIScope(tool), s.optionalCLIStatus(tool)).ctx
}

func (s *app) replaceOptionalCLI(tool string, scope desktopOptionalCLIScope, status application.OptionalCLIStatus) *desktopOptionalCLIState {
	jobs := s.ensureCLIJobs()
	if jobs.optional == nil {
		jobs.optional = make(map[string]*desktopOptionalCLIState)
	}
	s.retireOptionalCLI(tool)
	ctx, cancel := context.WithCancel(context.Background())
	next := &desktopOptionalCLIState{scope: scope, ctx: ctx, cancel: cancel, status: status}
	jobs.optional[tool] = next
	return next
}

func (s *app) retireOptionalCLI(tool string) {
	jobs := s.toolsUnit.state.cliJobs
	if jobs == nil {
		return
	}
	previous := jobs.optional[tool]
	if previous == nil {
		return
	}
	delete(jobs.optional, tool)
	previous.cancel()
	if previous.status.Ready() {
		s.invalidateCLIJobs(tool)
		s.settingsUnit.stopCLIAuthPoll(tool)
		delete(s.settingsUnit.state.settingsCLIStatus, tool)
		delete(s.settingsUnit.state.settingsCLIErrors, tool)
		delete(s.settingsUnit.state.settingsCLISessions, tool)
		s.retireIntegrationViews(tool)
	}
}

func (s *app) retireOptionalCLIsAfterConfigReplacement(kind string) {
	switch kind {
	case "config.save", "config.import":
		for _, tool := range desktopOptionalCLITools {
			s.retireOptionalCLI(tool)
		}
		s.reconcileSidebarNavigation()
	}
}

func (s *app) observeOptionalCLIs() {
	for _, tool := range desktopOptionalCLITools {
		s.requestOptionalCLIProbe(tool, false)
	}
}

func (s *app) stopOptionalCLIProbes() {
	if jobs := s.toolsUnit.state.cliJobs; jobs != nil {
		for _, current := range jobs.optional {
			current.cancel()
		}
	}
}

func (s *app) requestOptionalCLIProbe(tool string, force bool) {
	if !desktopHasOptionalCLI(tool) {
		return
	}
	scope := s.optionalCLIScope(tool)
	current := s.currentOptionalCLI(tool)
	if current != nil && (!force || !scope.enabled || current.status.Phase == application.OptionalCLIProbing) {
		return
	}
	if current == nil {
		s.retireOptionalCLI(tool)
	}
	backend, supported := s.backend.(application.OptionalCLIBackend)
	win := s.win
	if !scope.enabled || !supported || win == nil || win.IsDestroyed() {
		return
	}
	jobs := s.ensureCLIJobs()
	if jobs.lifecycle.Closed() {
		return
	}
	request := s.replaceOptionalCLI(tool, scope, application.OptionalCLIStatus{Tool: tool, Phase: application.OptionalCLIProbing})
	lease, err := jobs.lifecycle.Begin(request.ctx, asyncjob.Options{CancelOnClose: true})
	if err != nil {
		request.cancel()
		return
	}
	win.Invalidate()
	go func() {
		result, err := backend.ProbeOptionalCLI(lease.Context(), tool, force)
		lease.Complete(asyncJobCleanupError(err))
		lease.Cancel()
		if request.ctx.Err() != nil {
			return
		}
		jobs.lifecycle.Post(func(callback func()) bool {
			if win.IsDestroyed() {
				return false
			}
			win.Update(callback)
			return true
		}, func() {
			if s.win == win && !win.IsDestroyed() {
				s.finishOptionalCLIProbe(tool, request, result, err)
			}
		})
	}()
}

func (s *app) finishOptionalCLIProbe(tool string, request *desktopOptionalCLIState, status application.OptionalCLIStatus, err error) {
	if request == nil || s.currentOptionalCLI(tool) != request || request.ctx.Err() != nil {
		return
	}
	if err != nil {
		status = application.OptionalCLIStatus{Phase: application.OptionalCLIUnavailable, Error: err.Error()}
	}
	status.Tool = tool
	if status.Ready() && s.applyOptionalCLIPathDiscovery(tool, request.scope.path, status) {
		request.scope = s.optionalCLIScope(tool)
	}
	request.status = status
	s.reconcileSidebarNavigation()
	if status.Ready() && tool == "lark" && s.page == "settings" && s.settingsCLIPageActive(tool) {
		s.requestSettingsCLIStatus(tool, false)
	}
}

// A probe can persist a previously blank executable path. Merge that single
// field into the current projection while preserving unrelated saves and
// drafts. The successful probe already verified the discovered command.
func (s *app) applyOptionalCLIPathDiscovery(tool, previousPath string, status application.OptionalCLIStatus) bool {
	if previousPath != "" || status.ConfiguredPath == "" || status.ConfiguredPath != status.Command {
		return false
	}
	config, err := s.settingsConfigMap()
	if err != nil {
		return false
	}
	config[tool+"ExecutablePath"] = status.ConfiguredPath
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return false
	}
	next := string(encoded)
	s.syncSettingsConfigInputs(s.settingsUnit.state.configJSON, next)
	s.settingsUnit.state.configJSON, s.snapshot.ConfigJSON = next, next
	return true
}
