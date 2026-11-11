package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/releasenotice"
)

type prefetchProbePhase struct {
	Mode                 string             `json:"mode"`
	RefreshPath          string             `json:"refreshPath,omitempty"`
	StartedAt            string             `json:"startedAt"`
	WarmupMS             int64              `json:"warmupMs"`
	WarmupCalls          map[string]int     `json:"warmupCalls"`
	PrepareMS            int64              `json:"prepareMs"`
	SendMS               int64              `json:"sendMs"`
	DispatchMS           int64              `json:"dispatchMs"`
	BeforeFirstMockRPCMS int64              `json:"beforeFirstMockRpcMs"`
	PhaseTotalMS         int64              `json:"phaseTotalMs"`
	Calls                map[string]int     `json:"calls"`
	CLITotalMS           map[string]int64   `json:"cliTotalMs"`
	WarmupCLITotalMS     map[string]int64   `json:"warmupCliTotalMs"`
	Events               []noticeProbeEvent `json:"events"`
	SourceReceiptMatched bool               `json:"sourceReceiptMatched"`
	Recipients           int                `json:"recipients"`
	Warning              string             `json:"warning,omitempty"`
	Error                string             `json:"error,omitempty"`
}

func noticeProbeCounts(t *testing.T, f *tagNoticeDispatchFixture) map[string]int {
	counts := map[string]int{}
	for _, phase := range []string{"verify", "local", "metadata", "contact", "send", "genbu", "meegle-config", "meegle-auth", "meegle-user"} {
		counts[phase] = f.count(t, phase)
	}
	if f.state.ReadOnlyHome == "" {
		counts["meegle-config"] = f.count(t, "meegleProfile")
		counts["meegle-auth"] = f.count(t, "meegleAuth")
		counts["meegle-user"] = f.count(t, "meegleUser")
	}
	return counts
}

// Opt-in production Dispatch probe. Every mode has a new isolated home and
// receipts. Warm modes execute the real refresh route, never a cache fixture.
// The existing strict read-only relay refuses writes and intercepts every im
// command locally, including sends to self. No real message RPC can execute.
func TestTagNoticePrefetchPerformanceProbe(t *testing.T) {
	output := os.Getenv("SW_NOTICE_PREFETCH_PERF_OUTPUT")
	if output == "" {
		t.Skip("opt-in permanent preparation probe")
	}
	started := time.Now()
	sourceHome := os.Getenv("SW_NOTICE_PERF_SOURCE_HOME")
	var receipt *releasenotice.Session
	if name := os.Getenv("SW_NOTICE_PERF_SOURCE_RECEIPT"); name != "" {
		data, err := os.ReadFile(name)
		if err != nil || json.Unmarshal(data, &receipt) != nil || receipt == nil {
			t.Fatal("invalid historical source receipt", err)
		}
	}
	report := struct {
		CachePolicy     string               `json:"cachePolicy"`
		Path            string               `json:"path"`
		RPC             string               `json:"rpc"`
		RPCMocked       bool                 `json:"rpcMocked"`
		RealMessages    bool                 `json:"realMessages"`
		RealReadOnlyCLI bool                 `json:"realReadOnlyCli"`
		Genbu           string               `json:"genbu"`
		StartedAt       string               `json:"startedAt"`
		TotalMS         int64                `json:"totalMs"`
		Phases          []prefetchProbePhase `json:"phases"`
	}{CachePolicy: "permanent; replaced only by successful explicit requirement refresh or a confirmed context change",
		Path: "App.Dispatch(tag.notice.prepare) -> JSON snapshot -> App.Dispatch(tag.notice.send) -> Store.SendTag",
		RPC:  "RPC mocked: local success only; installed CLIs cannot receive im commands", RPCMocked: true, RealReadOnlyCLI: sourceHome != "",
		Genbu: "source successful batch; fresh copied Genbu timestamps in isolated history (no real Genbu command)", StartedAt: started.UTC().Format(time.RFC3339Nano)}
	if os.Getenv("SW_NOTICE_PERF_LIVE_GENBU") == "1" {
		report.Genbu = "normal build freshness checks with read-only query-tag relay"
	}
	for _, mode := range []string{"cold", "refresh-first-click", "refresh-new-instance"} {
		phaseStarted := time.Now()
		var f *tagNoticeDispatchFixture
		if sourceHome == "" {
			f = newTagNoticeDispatchFixture(t, "TesterA")
		} else {
			f = readOnlyNoticeProbeFixture(t, sourceHome, os.Getenv("SW_NOTICE_PERF_TASK"), os.Getenv("SW_NOTICE_PERF_BATCH"), os.Getenv("SW_NOTICE_PERF_LIVE_GENBU") == "1")
		}
		phase := prefetchProbePhase{Mode: mode, StartedAt: phaseStarted.UTC().Format(time.RFC3339Nano), CLITotalMS: map[string]int64{}, WarmupCLITotalMS: map[string]int64{}}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		var err error
		if mode != "cold" {
			link := f.taskURL()
			if receipt != nil {
				link = receipt.URL
			} else if sourceHome != "" {
				cfg, loadErr := f.app.Config.Load()
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				entry, loadErr := f.app.Manifests.LoadTask(value(cfg.TaskRoot), os.Getenv("SW_NOTICE_PERF_TASK"))
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				link = entry.Manifest.RequirementLink
			}
			phase.RefreshPath = "App.Dispatch(requirements.metadata force=true fullRefresh=true) -> asynchronous notification preparation -> disk"
			warmStarted := time.Now()
			_, err = f.app.Dispatch(ctx, Action{Kind: "requirements.metadata", Payload: map[string]any{"url": link, "force": true, "fullRefresh": true}})
			if err == nil {
				err = f.app.Integrations.WaitTagNoticePreparations(ctx)
			}
			phase.WarmupMS = time.Since(warmStarted).Milliseconds()
			if err == nil && mode == "refresh-new-instance" {
				if closeErr := f.app.Integrations.CloseRequirementDocuments(ctx); closeErr != nil {
					t.Fatal(closeErr)
				}
				rebuilt, newErr := New(f.app.Paths.Home)
				if newErr != nil {
					t.Fatal(newErr)
				}
				f.app = rebuilt
			}
		}
		before := noticeProbeCounts(t, f)
		phase.WarmupCalls = before
		clickStarted := time.Now()
		if err == nil {
			prepared, prepareErr := f.app.Dispatch(ctx, f.action)
			err = prepareErr
			phase.PrepareMS = time.Since(clickStarted).Milliseconds()
			if err == nil {
				data, _ := json.Marshal(prepared.Data)
				var session releasenotice.Session
				if decodeErr := json.Unmarshal(data, &session); decodeErr != nil {
					t.Fatal(decodeErr)
				}
				phase.Recipients, phase.Warning = len(session.Recipients), session.Warning
				if receipt != nil {
					err = matchNoticeProbeReceipt(session, *receipt)
					phase.SourceReceiptMatched = err == nil
				}
				if err == nil {
					sendStarted := time.Now()
					_, err = f.app.Dispatch(ctx, f.sendAction(t, session))
					phase.SendMS = time.Since(sendStarted).Milliseconds()
				}
			}
		}
		phase.DispatchMS = time.Since(clickStarted).Milliseconds()
		if err != nil {
			phase.Error = err.Error()
		}
		phase.Calls = noticeProbeCounts(t, f)
		for name, n := range before {
			phase.Calls[name] -= n
		}
		modeDir := filepath.Join(output, mode)
		if err := os.MkdirAll(modeDir, 0700); err != nil {
			t.Fatal(err)
		}
		files, readErr := os.ReadDir(f.state.Log)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var first time.Time
		for _, file := range files {
			if strings.HasPrefix(file.Name(), "send-") && strings.HasSuffix(file.Name(), ".started") {
				info, _ := file.Info()
				if info != nil && (first.IsZero() || info.ModTime().Before(first)) {
					first = info.ModTime()
				}
			}
			if !strings.HasPrefix(file.Name(), "timing-") {
				continue
			}
			data, _ := os.ReadFile(filepath.Join(f.state.Log, file.Name()))
			_ = os.WriteFile(filepath.Join(modeDir, file.Name()), data, 0600)
			var event noticeProbeEvent
			if json.Unmarshal(data, &event) == nil {
				phase.Events = append(phase.Events, event)
				at, _ := time.Parse(time.RFC3339Nano, event.StartedAt)
				if at.Before(clickStarted) {
					phase.WarmupCLITotalMS[event.Phase] += event.DurationMS
				} else {
					phase.CLITotalMS[event.Phase] += event.DurationMS
				}
			}
		}
		slices.SortFunc(phase.Events, func(a, b noticeProbeEvent) int { return strings.Compare(a.StartedAt, b.StartedAt) })
		if !first.IsZero() {
			phase.BeforeFirstMockRPCMS = first.Sub(clickStarted).Milliseconds()
		}
		// Preserve the generated noncredential cache as evidence of real refresh
		// output. The source home, task, history and receipt remain read-only.
		cacheFiles, _ := os.ReadDir(filepath.Join(f.app.Paths.Cache, "tag-notice-preparation"))
		for _, file := range cacheFiles {
			if strings.HasSuffix(file.Name(), ".json") {
				data, _ := os.ReadFile(filepath.Join(f.app.Paths.Cache, "tag-notice-preparation", file.Name()))
				_ = os.WriteFile(filepath.Join(modeDir, "cache-"+file.Name()), data, 0600)
			}
		}
		cancel()
		phase.PhaseTotalMS = time.Since(phaseStarted).Milliseconds()
		report.Phases = append(report.Phases, phase)
	}
	report.TotalMS = time.Since(started).Milliseconds()
	data, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "results.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log(string(data))
	for _, phase := range report.Phases {
		if phase.Error != "" {
			t.Errorf("%s: %s", phase.Mode, phase.Error)
		}
	}
}
