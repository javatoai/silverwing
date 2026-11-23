package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"silverwing-go/internal/application"
)

func enableOptionalCLIsForDesktopTest(t *testing.T, s *app, tools ...string) {
	t.Helper()
	config := map[string]any{}
	if source := firstNonEmpty(s.settingsUnit.state.configJSON, s.snapshot.ConfigJSON); source != "" {
		if err := json.Unmarshal([]byte(source), &config); err != nil {
			t.Fatal(err)
		}
	}
	optional, ok := config["optionalCLIs"].(map[string]any)
	if !ok {
		optional = map[string]any{}
		config["optionalCLIs"] = optional
	}
	for _, tool := range tools {
		optional[tool] = true
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	s.settingsUnit.state.configJSON, s.snapshot.ConfigJSON = string(encoded), string(encoded)
}

type optionalCLIDesktopProbeCall struct {
	tool  string
	force bool
	ctx   context.Context
}

type optionalCLIDesktopProbeBackend struct {
	application.Backend
	calls chan optionalCLIDesktopProbeCall
}

func (b optionalCLIDesktopProbeBackend) ProbeOptionalCLI(ctx context.Context, tool string, force bool) (application.OptionalCLIStatus, error) {
	b.calls <- optionalCLIDesktopProbeCall{tool: tool, force: force, ctx: ctx}
	<-ctx.Done()
	return application.OptionalCLIStatus{}, ctx.Err()
}

func (b optionalCLIDesktopProbeBackend) OptionalCLIStatus(tool string) application.OptionalCLIStatus {
	return application.OptionalCLIStatus{Tool: tool, Phase: application.OptionalCLIUnavailable}
}

func TestDesktopOptionalCLIProbeTracksAppliedConfig(t *testing.T) {
	backend := optionalCLIDesktopProbeBackend{calls: make(chan optionalCLIDesktopProbeCall, 8)}
	s := newApp(backend, Options{Page: "settings", CapturePath: "optional-cli-test"})
	s.win, s.loading = &mygo.Window{}, false
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeCLIJobs(ctx); err != nil {
			t.Error(err)
		}
	})
	waitCall := func(tool string, force bool) optionalCLIDesktopProbeCall {
		t.Helper()
		select {
		case call := <-backend.calls:
			if call.tool != tool || call.force != force {
				t.Fatalf("unexpected probe: %+v", call)
			}
			return call
		case <-time.After(time.Second):
			t.Fatalf("missing %s probe", tool)
			return optionalCLIDesktopProbeCall{}
		}
	}
	firstHome := t.TempDir()
	s.applySnapshot(application.Snapshot{Home: firstHome, ConfigJSON: `{}`})
	if s.toolsUnit.state.cliJobs != nil || s.optionalCLIReady("lark") {
		t.Fatal("disabled default entered the probe lifecycle")
	}
	enabled := application.Snapshot{Home: firstHome, ConfigJSON: `{"optionalCLIs":{"lark":true}}`}
	s.applySnapshot(enabled)
	first := waitCall("lark", false)
	if s.optionalCLIReady("lark") || s.optionalCLIStatus("lark").Phase != application.OptionalCLIProbing {
		t.Fatal("pending version probe exposed the integration")
	}
	firstRequest := s.currentOptionalCLI("lark")
	s.applySnapshot(enabled)
	if s.currentOptionalCLI("lark") != firstRequest {
		t.Fatal("unchanged snapshot started a second probe")
	}
	enabled.ConfigJSON = `{"optionalCLIs":{"lark":true},"larkExecutablePath":"new-lark"}`
	s.applySnapshot(enabled)
	second := waitCall("lark", false)
	if !errors.Is(first.ctx.Err(), context.Canceled) {
		t.Fatal("path change kept the old probe alive")
	}
	s.finishOptionalCLIProbe("lark", firstRequest, application.OptionalCLIStatus{Phase: application.OptionalCLIAvailable}, nil)
	if s.optionalCLIReady("lark") {
		t.Fatal("old path result restored Lark")
	}
	enabled.Home = t.TempDir()
	s.applySnapshot(enabled)
	third := waitCall("lark", false)
	if !errors.Is(second.ctx.Err(), context.Canceled) {
		t.Fatal("home change kept the old probe alive")
	}
	s.retireOptionalCLIsAfterConfigReplacement("config.import")
	s.applySnapshot(enabled)
	fourth := waitCall("lark", false)
	if !errors.Is(third.ctx.Err(), context.Canceled) {
		t.Fatal("configuration import kept the old probe alive")
	}
	s.finishOptionalCLIProbe("lark", s.currentOptionalCLI("lark"), application.OptionalCLIStatus{}, errors.New("test unavailable"))
	s.requestOptionalCLIProbe("lark", true)
	fifth := waitCall("lark", true)
	if !errors.Is(fourth.ctx.Err(), context.Canceled) {
		t.Fatal("retry kept the old probe alive")
	}
	enabled.ConfigJSON = `{"optionalCLIs":{"lark":false},"larkExecutablePath":"new-lark"}`
	s.applySnapshot(enabled)
	if !errors.Is(fifth.ctx.Err(), context.Canceled) || s.optionalCLIStatus("lark").Phase != application.OptionalCLIDisabled {
		t.Fatal("explicit off retained the running probe")
	}
}
