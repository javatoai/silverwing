package platform

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

type codexRPCFixtureEvent struct {
	Method           string `json:"method"`
	ID               int    `json:"id"`
	PID              int    `json:"pid"`
	CWD, Home, Proxy string
	Params           map[string]any `json:"params"`
}

func init() {
	root := os.Getenv("SW_CODEX_RPC_HELPER")
	if root == "" {
		return
	}
	if len(os.Args) < 2 || os.Args[1] != "app-server" {
		os.Exit(2)
	}
	log, err := os.OpenFile(filepath.Join(root, "rpc.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(2)
	}
	defer log.Close()
	mode, _ := os.ReadFile(filepath.Join(root, "mode"))
	cwd, _ := os.Getwd()
	record := func(method string, id int, params map[string]any) {
		_ = json.NewEncoder(log).Encode(codexRPCFixtureEvent{Method: method, ID: id, PID: os.Getpid(), CWD: cwd,
			Home: os.Getenv("CODEX_HOME"), Proxy: os.Getenv("HTTPS_PROXY"), Params: params})
	}
	record("$start", 0, nil)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var message struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			os.Exit(3)
		}
		record(message.Method, message.ID, message.Params)
		if message.Method == "initialized" {
			continue
		}
		if message.Method == "fixture/hang" {
			for {
				time.Sleep(time.Minute)
			}
		}
		if message.Method == "config/batchWrite" {
			os.Exit(11)
		} // Indeterminate write response: never replay.
		if string(mode) == "initialize-error" && message.Method == "initialize" || string(mode) == "skills-error" && message.Method == "skills/list" {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": message.ID, "error": map[string]any{"code": -32001, "message": "fixture unavailable"}})
			continue
		}
		result := map[string]any{}
		switch message.Method {
		case "fixture/payload":
			size, _ := message.Params["bytes"].(float64)
			if message.Params["stream"] == "stderr" {
				_, _ = os.Stderr.WriteString(strings.Repeat("d", int(size)))
				for {
					time.Sleep(time.Minute)
				}
			}
			result = map[string]any{"payload": strings.Repeat("x", int(size))}
		case "config/read":
			config := map[string]any{"mcp_servers": map[string]any{"fixture": map[string]any{"command": "fixture"}}}
			result = map[string]any{"config": config, "layers": []any{map[string]any{"name": map[string]any{"type": "user", "file": filepath.Join(os.Getenv("CODEX_HOME"), "config.toml")}, "config": config, "version": "fixture-version"}}}
		case "skills/list":
			result = map[string]any{"data": []any{map[string]any{"skills": []any{map[string]any{"name": "fixture", "path": filepath.Join(cwd, "SKILL.md"), "enabled": true}}}}}
		case "mcpServerStatus/list":
			name := "first"
			if message.Params["cursor"] == "page-2" {
				name = "second"
			} else {
				result["nextCursor"] = "page-2"
			}
			result["data"] = []any{map[string]any{"name": name, "tools": map[string]any{"fixture": map[string]any{}}}}
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": message.ID, "result": result})
	}
	os.Exit(0)
}

type codexRPCFixture struct {
	service   *Service
	config    domain.AppConfig
	root, cwd string
}

func newCodexRPCFixture(t *testing.T) codexRPCFixture {
	t.Helper()
	root := t.TempDir()
	cwd, codexHome := filepath.Join(root, "task"), filepath.Join(root, "codex-home")
	for _, directory := range []string{cwd, codexHome} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("# isolated fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SW_CODEX_RPC_HELPER", root)
	t.Setenv("CODEX_HOME", codexHome)
	cli, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	paths := storage.NewPaths(filepath.Join(root, "home"))
	store := storage.NewConfigStore(paths)
	config := domain.DefaultAppConfig()
	config.CodexExecutablePath = &cli
	proxy := "http://127.0.0.1:12345"
	config.CommandProxyURL = &proxy
	config.CommandProxyTargets = []domain.CommandProxyTarget{domain.ProxyTargetCodex}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	config, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return codexRPCFixture{service: NewService(paths, store, nil), config: config, root: root, cwd: cwd}
}

func (f codexRPCFixture) events(t *testing.T) []codexRPCFixtureEvent {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.root, "rpc.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var events []codexRPCFixtureEvent
	complete := string(data)
	if last := strings.LastIndexByte(complete, '\n'); last >= 0 {
		complete = complete[:last]
	} else {
		return nil
	}
	for _, line := range strings.Split(complete, "\n") {
		if line == "" {
			continue
		}
		var event codexRPCFixtureEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

func codexRPCMethodCounts(events []codexRPCFixtureEvent) map[string]int {
	counts := map[string]int{}
	for _, event := range events {
		counts[event.Method]++
	}
	return counts
}

func TestCodexSnapshotReusesOneInitializedSessionAndLoadsSkillsOnDemand(t *testing.T) {
	for _, includeSkills := range []bool{false, true} {
		t.Run(fmt.Sprintf("skills-%v", includeSkills), func(t *testing.T) {
			f := newCodexRPCFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			value, err := f.service.Execute(ctx, "codex.ai/snapshot", map[string]any{"cwd": f.cwd, "includeSkills": includeSkills})
			if err != nil {
				t.Fatal(err)
			}
			snapshot := value.(map[string]any)
			skills := snapshot["skills"].([]codexAISkill)
			wantSkills := 0
			if includeSkills {
				wantSkills = 1
			}
			counts := codexRPCMethodCounts(f.events(t))
			if counts["$start"] != 1 || counts["initialize"] != 1 || counts["initialized"] != 1 || counts["config/read"] != 1 || counts["skills/list"] != wantSkills || len(skills) != wantSkills {
				t.Fatalf("snapshot repeated startup/handshake or eagerly loaded skills: counts=%v snapshot=%+v", counts, snapshot)
			}
			for _, event := range f.events(t) {
				if !samePath(event.CWD, f.cwd) || event.Home != os.Getenv("CODEX_HOME") || !strings.Contains(event.Proxy, "127.0.0.1:12345") {
					t.Fatalf("session scope escaped: %+v", event)
				}
			}
			assertCodexRPCFixtureExited(t, f.events(t)[0].PID)
			// No process or handshake is retained between user operations.
			if _, err := f.service.Execute(ctx, "codex.ai/snapshot", map[string]any{"cwd": f.cwd}); err != nil {
				t.Fatal(err)
			}
			if got := codexRPCMethodCounts(f.events(t))["$start"]; got != 2 {
				t.Fatalf("session escaped one operation: starts=%d", got)
			}
		})
	}
}

func TestCodexMCPCheckReusesOneSessionForConfigAndAllPages(t *testing.T) {
	f := newCodexRPCFixture(t)
	value, err := f.service.Execute(context.Background(), "codex.extensions/mcp/check", map[string]any{"cwd": f.cwd})
	if err != nil {
		t.Fatal(err)
	}
	connections := value.(map[string]any)["connections"].(map[string]map[string]any)
	counts := codexRPCMethodCounts(f.events(t))
	if len(connections) != 2 || counts["$start"] != 1 || counts["initialize"] != 1 || counts["config/read"] != 1 || counts["mcpServerStatus/list"] != 2 {
		t.Fatalf("MCP pagination repeated startup or dropped results: counts=%v connections=%+v", counts, connections)
	}
	ids := map[int]bool{}
	for _, event := range f.events(t) {
		if event.ID > 0 {
			if ids[event.ID] {
				t.Fatalf("RPC request IDs reused: %+v", f.events(t))
			}
			ids[event.ID] = true
		}
	}
	assertCodexRPCFixtureExited(t, f.events(t)[0].PID)
}

func TestCodexRPCSessionRejectsChangedScopeBeforeNextRequest(t *testing.T) {
	for _, changed := range []string{"cwd", "home", "executable", "proxy", "saved-config", "after-last-response"} {
		t.Run(changed, func(t *testing.T) {
			f := newCodexRPCFixture(t)
			_, err := f.service.withCodexRPCSession(context.Background(), f.config, f.cwd, nil, 10*time.Second, true, func(ctx context.Context) (any, error) {
				if _, err := f.service.rpcRequest(ctx, f.config, f.cwd, nil, "config/read", nil, time.Second); err != nil {
					return nil, err
				}
				cfg, cwd := f.config, f.cwd
				var home *string
				switch changed {
				case "cwd":
					cwd = filepath.Join(f.root, "other")
				case "home":
					value := filepath.Join(f.root, "other-home")
					home = &value
				case "executable":
					value := filepath.Join(f.root, "other.exe")
					cfg.CodexExecutablePath = &value
				case "proxy":
					value := "http://127.0.0.1:23456"
					cfg.CommandProxyURL = &value
				case "saved-config", "after-last-response":
					if _, err := f.service.config.Update(func(config domain.AppConfig) (domain.AppConfig, error) {
						value := "http://127.0.0.1:23456"
						config.CommandProxyURL = &value
						return config, nil
					}); err != nil {
						return nil, err
					}
					if changed == "after-last-response" {
						return map[string]any{}, nil
					}
				}
				return f.service.rpcRequest(ctx, cfg, cwd, home, "skills/list", nil, time.Second)
			})
			if err == nil || !strings.Contains(err.Error(), "身份已变化") {
				t.Fatalf("changed %s scope was reused: %v", changed, err)
			}
			counts := codexRPCMethodCounts(f.events(t))
			if counts["$start"] != 1 || counts["config/read"] != 1 || counts["skills/list"] != 0 {
				t.Fatalf("changed scope sent/replayed a request: %v", counts)
			}
			assertCodexRPCFixtureExited(t, f.events(t)[0].PID)
		})
	}
}

func TestCodexRPCSessionCancellationAndTimeoutJoinProcess(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			f := newCodexRPCFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				requestTimeout := time.Minute
				if mode == "timeout" {
					requestTimeout = 50 * time.Millisecond
				}
				_, err := f.service.withCodexRPCSession(ctx, f.config, f.cwd, nil, 10*time.Second, true, func(ctx context.Context) (any, error) {
					return f.service.rpcRequest(ctx, f.config, f.cwd, nil, "fixture/hang", nil, requestTimeout)
				})
				done <- err
			}()
			if mode == "cancel" {
				deadline := time.After(5 * time.Second)
				for codexRPCMethodCounts(f.events(t))["fixture/hang"] == 0 {
					select {
					case <-deadline:
						t.Fatal("fixture did not receive request")
					case <-time.After(10 * time.Millisecond):
					}
				}
				cancel()
			}
			select {
			case err := <-done:
				want := context.Canceled
				if mode == "timeout" {
					want = context.DeadlineExceeded
				}
				if !errors.Is(err, want) {
					t.Fatalf("cancellation/timeout lost: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("session did not settle and join cleanup")
			}
			counts := codexRPCMethodCounts(f.events(t))
			if counts["$start"] != 1 || counts["fixture/hang"] != 1 {
				t.Fatalf("cancellation replayed RPC: %v", counts)
			}
			assertCodexRPCFixtureExited(t, f.events(t)[0].PID)
		})
	}
}

func TestCodexRPCWriteWithLostResponseIsNeverReplayed(t *testing.T) {
	f := newCodexRPCFixture(t)
	_, err := f.service.rpcRequest(context.Background(), f.config, f.cwd, nil, "config/batchWrite", map[string]any{"edits": []any{}}, 5*time.Second)
	if err == nil {
		t.Fatal("indeterminate write claimed success")
	}
	counts := codexRPCMethodCounts(f.events(t))
	if counts["$start"] != 1 || counts["initialize"] != 1 || counts["config/batchWrite"] != 1 {
		t.Fatalf("indeterminate write was replayed: %v", counts)
	}
	assertCodexRPCFixtureExited(t, f.events(t)[0].PID)
}

func TestCodexRPCInitializationAndOptionalSkillErrorsKeepExistingBehavior(t *testing.T) {
	for _, mode := range []string{"initialize-error", "skills-error"} {
		t.Run(mode, func(t *testing.T) {
			f := newCodexRPCFixture(t)
			if err := os.WriteFile(filepath.Join(f.root, "mode"), []byte(mode), 0600); err != nil {
				t.Fatal(err)
			}
			value, err := f.service.Execute(context.Background(), "codex.ai/snapshot", map[string]any{"cwd": f.cwd, "includeSkills": true})
			if mode == "initialize-error" {
				if err == nil || codexRPCMethodCounts(f.events(t))["config/read"] != 0 {
					t.Fatalf("initialization failure continued: value=%+v err=%v", value, err)
				}
			} else if err != nil || len(value.(map[string]any)["warnings"].([]string)) == 0 {
				t.Fatalf("optional skill error became fatal or hidden: value=%+v err=%v", value, err)
			}
			assertCodexRPCFixtureExited(t, f.events(t)[0].PID)
		})
	}
}

func TestCodexRPCSessionReceivesTwoLargeLegalFrames(t *testing.T) {
	f := newCodexRPCFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const payloadBytes = 2_100_000
	value, err := f.service.withCodexRPCSession(ctx, f.config, f.cwd, nil, 30*time.Second, true, func(ctx context.Context) (any, error) {
		lengths := make([]int, 0, 2)
		for frame := 0; frame < 2; frame++ {
			response, err := f.service.rpcRequest(ctx, f.config, f.cwd, nil, "fixture/payload", map[string]any{"bytes": payloadBytes}, 30*time.Second)
			if err != nil {
				return nil, err
			}
			payload, ok := response["payload"].(string)
			if !ok || len(payload) != payloadBytes || payload[0] != 'x' || payload[len(payload)-1] != 'x' {
				return nil, fmt.Errorf("large frame %d was incomplete: type=%T bytes=%d", frame, response["payload"], len(payload))
			}
			lengths = append(lengths, len(payload))
		}
		return lengths, nil
	})
	if err != nil {
		t.Fatalf("two complete legal frames exhausted a per-request capture budget: %v", err)
	}
	lengths := value.([]int)
	if len(lengths) != 2 || lengths[0] != payloadBytes || lengths[1] != payloadBytes {
		t.Fatalf("large frame results changed: %v", lengths)
	}
	counts := codexRPCMethodCounts(f.events(t))
	if counts["$start"] != 1 || counts["initialize"] != 1 || counts["fixture/payload"] != 2 {
		t.Fatalf("large frames restarted/replayed the session: %v", counts)
	}
	assertCodexRPCFixtureExited(t, f.events(t)[0].PID)
}

func TestCodexRPCOutputBudgetFailuresAreExplicitAndJoinProcess(t *testing.T) {
	for _, tc := range []struct {
		name, stream, label string
		bytes, frames       int
	}{
		{name: "cumulative", bytes: 3_500_000, frames: 10, label: "累计标准输出"},
		{name: "complete-frame", bytes: 4_000_001, frames: 1, label: "单帧"},
		{name: "diagnostic", stream: "stderr", bytes: 2*1024*1024 + 1, frames: 1, label: "诊断输出"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCodexRPCFixture(t)
			// Ordinary requests get 30s; the external deadline bounds this
			// regression test but must never be the reported overflow cause.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := f.service.withCodexRPCSession(ctx, f.config, f.cwd, nil, 30*time.Second, true, func(ctx context.Context) (any, error) {
				for frame := 0; frame < tc.frames; frame++ {
					if _, err := f.service.rpcRequest(ctx, f.config, f.cwd, nil, "fixture/payload", map[string]any{"bytes": tc.bytes, "stream": tc.stream}, 30*time.Second); err != nil {
						return nil, err
					}
				}
				return nil, nil
			})
			if !errors.Is(err, errCodexRPCOutputLimit) || !strings.Contains(err.Error(), tc.label) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("output overflow waited for RPC timeout or lost its budget cause: %v", err)
			}
			if ctx.Err() != nil {
				t.Fatalf("overflow did not terminate before the regression deadline: %v", ctx.Err())
			}
			counts := codexRPCMethodCounts(f.events(t))
			if counts["$start"] != 1 || counts["initialize"] != 1 || counts["fixture/payload"] != tc.frames {
				t.Fatalf("overflow replayed or continued requests: %v", counts)
			}
			assertCodexRPCFixtureExited(t, f.events(t)[0].PID)
		})
	}
}

func TestCodexRPCOutputFailureKeepsCleanupError(t *testing.T) {
	cleanup := &process.CleanupError{Phase: "fixture cleanup", Cause: errors.New("fixture descendant still running")}
	err := codexRPCOutputFailure(domain.DefaultAppConfig(), process.Result{StdoutTruncated: true,
		Stdout: `{"credential":"fixture-secret-json"}`, Stderr: "fixture-secret-stderr"}, cleanup, context.Canceled, nil)
	if !errors.Is(err, errCodexRPCOutputLimit) || !process.IsCleanupFailure(err) || !errors.Is(err, context.Canceled) {
		t.Fatalf("explicit output budget error discarded cleanup uncertainty: %v", err)
	}
	if !strings.Contains(err.Error(), "进程清理未确认") || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("output failure hid cleanup uncertainty or exposed captured payload: %v", err)
	}
}
