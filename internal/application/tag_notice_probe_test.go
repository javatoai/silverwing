package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/integrations"
	"silverwing-go/internal/larkcli"
	"silverwing-go/internal/process"
	"silverwing-go/internal/releasenotice"
	"silverwing-go/internal/storage"
)

type noticeProbeRunner struct {
	base    *process.Runner
	backend string
}

type noticeProbeEvent struct {
	Phase      string `json:"phase"`
	StartedAt  string `json:"startedAt"`
	DurationMS int64  `json:"durationMs"`
	ExitCode   int    `json:"exitCode"`
	Error      string `json:"error,omitempty"`
	RPCMocked  bool   `json:"rpcMocked,omitempty"`
}

func writeNoticeProbeEvent(log string, event noticeProbeEvent) {
	if file, err := os.CreateTemp(log, "timing-*.json"); err == nil {
		_ = json.NewEncoder(file).Encode(event)
		_ = file.Close()
	}
}

func (r noticeProbeRunner) Run(ctx context.Context, spec process.Spec) (process.Result, error) {
	if r.backend != "" {
		if spec.Env == nil {
			spec.Env = map[string]string{}
		}
		spec.Env["SILVERWING_TAG_NOTICE_FIXTURE"] = r.backend
	}
	return r.base.Run(ctx, spec)
}

func readNoticeProbeConfig(ctx context.Context, home string) (domain.AppConfig, error) {
	store := storage.NewConfigStore(storage.NewPaths(home))
	for {
		config, err := store.LoadForPreview(ctx)
		if !errors.Is(err, storage.ErrLockBusy) {
			return config, err
		}
		// Relay processes may read together; ConfigStore's cross-process reader
		// gate is exclusive/nonblocking. Retry only that transient reader clash.
		select {
		case <-ctx.Done():
			return domain.AppConfig{}, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// Opt-in probe relay. The only route for im commands is the fake responder in
// runNoticeFixtureCLI. Every forwarded command below is an explicit read.
func runNoticeReadOnlyCLI(state noticeCLIFixtureState, args []string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	config, err := readNoticeProbeConfig(ctx, state.ReadOnlyHome)
	if err != nil || ReleaseNoticeConfigIdentity(config) != state.SourceConfigID {
		fmt.Fprintln(os.Stderr, "read-only probe: source configuration changed or is unavailable")
		return 80
	}
	phase, target, tool := "", domain.ProxyTargetMeegle, executable.ToolMeegle
	switch {
	case len(args) >= 2 && args[0] == "auth" && args[1] == "status" && slices.Contains(args, "--profile"):
		phase = "local"
		if slices.Contains(args, "--verify") {
			phase = "verify"
		}
	case len(args) >= 2 && args[0] == "contact" && args[1] == "+search-user":
		phase = "contact"
	case len(args) >= 2 && args[0] == "workitem" && args[1] == "get":
		phase = "metadata"
	case len(args) >= 3 && args[0] == "config" && args[1] == "profile" && args[2] == "current":
		phase = "meegle-config"
	case len(args) >= 2 && args[0] == "user" && args[1] == "me":
		phase = "meegle-user"
	case len(args) >= 2 && args[0] == "auth" && args[1] == "status":
		phase = "meegle-auth"
	case len(args) > 0 && args[0] == "query-tag" && state.LiveGenbu:
		phase, target, tool = "genbu", domain.ProxyTargetGenbu, executable.ToolGenbu
	default:
		fmt.Fprintln(os.Stderr, "read-only probe refused a non-whitelisted command")
		return 81
	}
	started := time.Now()
	marker, err := os.CreateTemp(state.Log, phase+"-*.started")
	if err != nil {
		return 82
	}
	_ = marker.Close()
	runner := noticeProbeRunner{base: process.NewRunner(), backend: state.BackendFixture}
	var result process.Result
	if phase == "local" || phase == "verify" || phase == "contact" {
		// Client.Run applies the same native launcher and inherited-environment
		// policy as production. Lark never receives the SW command proxy.
		result, err = (larkcli.Client{Runner: runner, Config: config, AsUser: true}).Run(ctx, args, 30*time.Second)
	} else {
		command := config.MeegleExecutablePath
		if target == domain.ProxyTargetGenbu {
			command = config.GenbuExecutablePath
		}
		var spec process.Spec
		spec, err = commandruntime.Prepare(ctx, runner, &config, target, tool, value(command))
		if err == nil {
			spec.Arguments, spec.Timeout = args, 30*time.Second
			result, err = runner.Run(ctx, spec)
		}
	}
	event := noticeProbeEvent{Phase: phase, StartedAt: started.UTC().Format(time.RFC3339Nano), DurationMS: time.Since(started).Milliseconds(), ExitCode: result.ExitCode}
	if err != nil {
		event.Error = commandruntime.Redact(config, err.Error())
	}
	writeNoticeProbeEvent(state.Log, event)
	fmt.Fprint(os.Stdout, result.Stdout)
	fmt.Fprint(os.Stderr, result.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, event.Error)
		return 83
	}
	return result.ExitCode
}

func readOnlyNoticeProbeFixture(t *testing.T, sourceHome, taskID, batchID string, liveGenbu bool) *tagNoticeDispatchFixture {
	t.Helper()
	ctx := context.Background()
	config, err := storage.NewConfigStore(storage.NewPaths(sourceHome)).LoadForPreview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sourceConfigID := ReleaseNoticeConfigIdentity(config)
	entry, err := storage.NewManifestStore().LoadTask(value(config.TaskRoot), taskID)
	if err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Join(entry.Path, "tag-operations"))
	if err != nil {
		t.Fatal(err)
	}
	operations := []domain.TagOperation{}
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(entry.Path, "tag-operations", file.Name()))
		var op domain.TagOperation
		if err != nil || json.Unmarshal(data, &op) != nil {
			t.Fatal("invalid source history", file.Name(), err)
		}
		if value(op.BatchID) != batchID {
			continue
		}
		if !liveGenbu {
			if op.GenbuStatus.Build != domain.GenbuSuccess || op.GenbuStatus.NotFound || value(op.GenbuStatus.FailureReason) != "" {
				t.Fatal("fresh Genbu fixture requires a source batch already recorded as successful")
			}
			now := domain.FormatSilverWingTime(time.Now())
			op.GenbuStatus.CheckedAt = &now
		}
		operations = append(operations, op)
	}
	if len(operations) == 0 {
		t.Fatal("source batch not found")
	}
	paths := storage.NewPaths(t.TempDir())
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	profile := config.EffectiveLarkNotice().Profile
	config.TaskRoot, config.LarkExecutablePath, config.MeegleExecutablePath, config.GenbuExecutablePath = &paths.Tasks, &program, &program, &program
	// Any unexpected Git/AI action must reach the same rejecting whitelist,
	// rather than an installed tool inherited from the source configuration.
	config.GitExecutablePath, config.CodexExecutablePath = &program, &program
	// Real proxy policy is applied in the read-only relay, not persisted into
	// the temporary app. No credentials or original receipts enter the report.
	config.CommandProxyURL, config.CommandProxyUsername, config.CommandProxyPassword, config.CommandProxyNoProxy = nil, nil, nil, nil
	config.CommandProxyTargets = nil
	a := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore(), Runner: process.NewRunner()}
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	config, _ = a.Config.Load()
	taskPath := filepath.Join(paths.Tasks, taskID)
	if err := a.Manifests.Save(taskPath, entry.Manifest); err != nil {
		t.Fatal(err)
	}
	for _, op := range operations {
		saveTagProbeFixture(t, taskPath, op)
	}
	a.Integrations = integrations.NewService(paths, a.Config, a.Runner)
	state := noticeCLIFixtureState{Log: filepath.Join(paths.Home, "probe-cli-log"), Profile: profile, ReadOnlyHome: sourceHome, SourceConfigID: sourceConfigID, LiveGenbu: liveGenbu}
	if err := os.MkdirAll(state.Log, 0700); err != nil {
		t.Fatal(err)
	}
	f := &tagNoticeDispatchFixture{app: a, path: filepath.Join(paths.Home, "probe-fixture.json"), state: state,
		action: Action{Kind: "tag.notice.prepare", TaskID: taskID, Payload: map[string]any{"tagHistoryHome": paths.Home, "tagHistoryTaskRoot": paths.Tasks, "tagHistoryTaskPath": taskPath, "tagHistoryTaskCreatedAt": entry.Manifest.CreatedAt, "groupId": batchID, "noticeConfigIdentity": ReleaseNoticeConfigIdentity(config)}}}
	f.saveState(t)
	t.Setenv("SILVERWING_TAG_NOTICE_FIXTURE", f.path)
	t.Cleanup(func() { _ = f.app.Integrations.CloseRequirementDocuments(context.Background()) })
	return f
}

// Run only through build/dispatch-notice-performance/probe.ps1. Normal tests
// skip it, and fixture mode never reads real user config or invokes real CLIs.
func TestTagNoticePerformanceProbe(t *testing.T) {
	probeStarted := time.Now()
	output := os.Getenv("SW_NOTICE_PERF_OUTPUT")
	if output == "" {
		t.Skip("opt-in isolated notification performance probe")
	}
	sourceHome := os.Getenv("SW_NOTICE_PERF_SOURCE_HOME")
	liveGenbu := os.Getenv("SW_NOTICE_PERF_LIVE_GENBU") == "1"
	var sourceReceipt *releasenotice.Session
	if path := os.Getenv("SW_NOTICE_PERF_SOURCE_RECEIPT"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(data, &sourceReceipt) != nil || sourceReceipt == nil {
			t.Fatal("cannot read source receipt", err)
		}
		if sourceReceipt.Profile == "" || sourceReceipt.Identity.OpenID == "" || len(sourceReceipt.Rows) == 0 || len(sourceReceipt.Recipients) == 0 {
			t.Fatal("source receipt lacks a user notification snapshot")
		}
	}
	var f *tagNoticeDispatchFixture
	if sourceHome == "" {
		f = newTagNoticeDispatchFixture(t, "TesterA")
	} else {
		if os.Getenv("SW_NOTICE_PERF_TASK") == "" || os.Getenv("SW_NOTICE_PERF_BATCH") == "" {
			t.Fatal("read-only probe requires task and batch")
		}
		f = readOnlyNoticeProbeFixture(t, sourceHome, os.Getenv("SW_NOTICE_PERF_TASK"), os.Getenv("SW_NOTICE_PERF_BATCH"), liveGenbu)
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	report := struct {
		Path                 string             `json:"path"`
		RealMessages         bool               `json:"realMessages"`
		RPC                  string             `json:"rpc"`
		RPCMocked            bool               `json:"rpcMocked"`
		RealReadOnlyCLI      bool               `json:"realReadOnlyCli"`
		SourceReceiptMatched bool               `json:"sourceReceiptMatched,omitempty"`
		Genbu                string             `json:"genbu"`
		StartedAt            string             `json:"startedAt"`
		SetupMS              int64              `json:"setupMs"`
		PrepareMS            int64              `json:"prepareMs"`
		SendMS               int64              `json:"sendMs"`
		DispatchTotalMS      int64              `json:"dispatchTotalMs"`
		ProbeTotalMS         int64              `json:"probeTotalMs"`
		BeforeFirstDrySendMS int64              `json:"beforeFirstDrySendMs"`
		Calls                map[string]int     `json:"calls"`
		CLITotalMS           map[string]int64   `json:"cliTotalMs"`
		Events               []noticeProbeEvent `json:"events,omitempty"`
		Recipients           int                `json:"recipients"`
		FallbackWarning      string             `json:"fallbackWarning,omitempty"`
		Error                string             `json:"error,omitempty"`
	}{Path: "App.Dispatch(tag.notice.prepare) -> JSON snapshot -> App.Dispatch(tag.notice.send) -> Store.SendTag", RPC: "RPC mocked: local success; no real message request", RPCMocked: true, RealReadOnlyCLI: sourceHome != "", Genbu: "fresh successful fixture", Calls: map[string]int{}, CLITotalMS: map[string]int64{}}
	if liveGenbu {
		report.Genbu = "normal freshness policy; read-only query-tag relay"
	}
	started := time.Now()
	report.StartedAt, report.SetupMS = started.UTC().Format(time.RFC3339Nano), started.Sub(probeStarted).Milliseconds()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	prepared, err := f.app.Dispatch(ctx, f.action)
	report.PrepareMS = time.Since(started).Milliseconds()
	if err == nil {
		data, encodeErr := json.Marshal(prepared.Data)
		var session releasenotice.Session
		if encodeErr != nil || json.Unmarshal(data, &session) != nil {
			t.Fatal("invalid prepare snapshot", encodeErr)
		}
		report.Recipients, report.FallbackWarning = len(session.Recipients), session.Warning
		if sourceReceipt != nil {
			err = matchNoticeProbeReceipt(session, *sourceReceipt)
			report.SourceReceiptMatched = err == nil
		}
		if err == nil {
			sendStarted := time.Now()
			_, err = f.app.Dispatch(ctx, f.sendAction(t, session))
			report.SendMS = time.Since(sendStarted).Milliseconds()
		}
	}
	report.DispatchTotalMS = time.Since(started).Milliseconds()
	if err != nil {
		report.Error = err.Error()
	}
	for _, phase := range []string{"verify", "metadata", "contact", "local", "send", "genbu"} {
		report.Calls[phase] = f.count(t, phase)
	}
	files, readErr := os.ReadDir(f.state.Log)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var firstSend time.Time
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "send-") && strings.HasSuffix(file.Name(), ".started") {
			info, _ := file.Info()
			if info != nil && (firstSend.IsZero() || info.ModTime().Before(firstSend)) {
				firstSend = info.ModTime()
			}
		}
		if strings.HasPrefix(file.Name(), "timing-") {
			data, _ := os.ReadFile(filepath.Join(f.state.Log, file.Name()))
			_ = os.WriteFile(filepath.Join(output, file.Name()), data, 0600)
			var event noticeProbeEvent
			if json.Unmarshal(data, &event) == nil {
				report.Events = append(report.Events, event)
				report.CLITotalMS[event.Phase] += event.DurationMS
			}
		}
	}
	slices.SortFunc(report.Events, func(a, b noticeProbeEvent) int { return strings.Compare(a.StartedAt, b.StartedAt) })
	if !firstSend.IsZero() {
		report.BeforeFirstDrySendMS = firstSend.Sub(started).Milliseconds()
	}
	report.ProbeTotalMS = time.Since(probeStarted).Milliseconds()
	data, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "results.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log(string(data))
	if err != nil {
		t.Fatal(err)
	}
}

// Compare the historical sender and batch, without importing its receipt or
// keys into the isolated store. A different latest recipient must not turn a
// claimed same-recipient timing sample into a self-fallback measurement.
func matchNoticeProbeReceipt(prepared, source releasenotice.Session) error {
	ids := func(session releasenotice.Session) []string {
		out := make([]string, 0, len(session.Recipients))
		for _, recipient := range session.Recipients {
			out = append(out, recipient.OpenID)
		}
		slices.Sort(out)
		return out
	}
	if prepared.Profile != source.Profile || prepared.Identity.AppID != source.Identity.AppID || prepared.Identity.OpenID != source.Identity.OpenID ||
		prepared.URL != source.URL || releasenotice.Digest(prepared.Rows) != releasenotice.Digest(source.Rows) || !slices.Equal(ids(prepared), ids(source)) {
		return errors.New("read-only probe: sender/profile/requirement/batch/recipients differ from source receipt")
	}
	return nil
}

func TestTagNoticeReadOnlyRelayUsesDispatchAndNeverForwardsMessages(t *testing.T) {
	for _, drift := range []bool{false, true} {
		t.Run(fmt.Sprintf("source-config-drift-%t", drift), func(t *testing.T) {
			source := newTagNoticeDispatchFixture(t, "TesterA")
			// If im ever reaches the underlying tool this would be a failure.
			source.state.Results["ou_a"] = "failed"
			source.saveState(t)
			beforeConfig := source.app.Config.FileSnapshot()
			if beforeConfig.ReadError != nil || beforeConfig.Content == nil {
				t.Fatal("cannot snapshot source config", beforeConfig.ReadError)
			}
			probe := readOnlyNoticeProbeFixture(t, source.app.Paths.Home, source.scope.TaskID, "notice-fixture-batch", false)
			cfg, err := probe.app.Config.Load()
			if err != nil || value(cfg.GitExecutablePath) != value(cfg.LarkExecutablePath) || value(cfg.CodexExecutablePath) != value(cfg.LarkExecutablePath) {
				t.Fatal("probe must intercept every configured CLI", err)
			}
			probe.state.BackendFixture = source.path
			probe.saveState(t)
			prepared := probe.prepare(t)
			if drift {
				config, err := source.app.Config.Load()
				if err != nil {
					t.Fatal(err)
				}
				config.LarkNotice.Profile = "changed-source-profile"
				if err := source.app.Config.Save(config); err != nil {
					t.Fatal(err)
				}
			}
			_, sendErr := probe.app.Dispatch(context.Background(), probe.sendAction(t, prepared))
			if (sendErr != nil) != drift || source.count(t, "send") != 0 {
				t.Fatal("read-only relay forwarded message or ignored source drift", sendErr)
			}
			if !drift {
				afterConfig := source.app.Config.FileSnapshot()
				if afterConfig.ReadError != nil || value(beforeConfig.Content) != value(afterConfig.Content) {
					t.Fatal("read-only probe modified source config", afterConfig.ReadError)
				}
				if probe.count(t, "verify") != 2 || probe.count(t, "metadata") != 1 || probe.count(t, "contact") != 1 || probe.count(t, "send") != 1 {
					t.Fatal("read-only relay did not measure optimized dispatch")
				}
			}
		})
	}
}
