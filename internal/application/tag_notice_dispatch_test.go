package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/integrations"
	"silverwing-go/internal/process"
	"silverwing-go/internal/releasenotice"
)

// Every external command in these dispatch tests is this test executable.
// Marker barriers assert overlap/order; timeouts only bound broken test runs.
type noticeCLIFixtureState struct {
	Log, Sender, Title                string
	Profile                           string
	Testers                           []string
	Contacts                          map[string]string
	Results                           map[string]string
	Parallel                          bool
	GatePhase, Gate                   string
	ReadOnlyHome                      string
	SourceConfigID                    string
	LiveGenbu                         bool
	BackendFixture                    string
	MeegleUser, MeegleProfile         string
	MetadataFailure, IncompleteRoster bool
	ListSize                          int
	GateAll                           bool
}

func init() {
	if path := os.Getenv("SILVERWING_TAG_NOTICE_FIXTURE"); path != "" && len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-test.") {
		os.Exit(runNoticeFixtureCLI(path, os.Args[1:]))
	}
}

func readNoticeFixtureState(path string) (noticeCLIFixtureState, error) {
	var state noticeCLIFixtureState
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &state)
	}
	return state, err
}

func waitNoticeFixtureFile(path string) error {
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			return fmt.Errorf("fixture barrier not released: %s", path)
		}
	}
}

func runNoticeFixtureCLI(path string, args []string) int {
	state, err := readNoticeFixtureState(path)
	if err != nil {
		return 90
	}
	if state.ReadOnlyHome != "" && args[0] != "im" {
		return runNoticeReadOnlyCLI(state, args)
	}
	profile := state.Profile
	if profile == "" {
		profile = "notice-fixture"
	}
	value := func(flag string) string {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == flag {
				return args[i+1]
			}
		}
		return ""
	}
	phase := ""
	switch args[0] {
	case "--version":
		fmt.Println("notice-fixture-1")
		return 0
	case "workflow":
		fmt.Println(`{"list":[],"pagination":{"page_num":1,"has_more":false}}`)
		return 0
	case "config":
		phase = "meegleProfile"
	case "user":
		phase = "meegleUser"
	case "auth":
		if value("--profile") == "" {
			phase = "meegleAuth"
			break
		}
		if value("--profile") != profile {
			return 91
		}
		phase = "local"
		if slices.Contains(args, "--verify") {
			phase = "verify"
		}
	case "workitem":
		if len(args) > 1 && args[1] != "get" {
			switch args[1] {
			case "meta-fields":
				fmt.Println(`{"list":[{"field_name":"Duration","field_type":"schedule","field_key":"duration"}],"pagination":{"page_num":1,"has_more":false}}`)
				return 0
			case "meta-roles":
				fmt.Println(`{"list":[{"role_id":"dev","role_name":"Dev Owner"}],"pagination":{"page_num":1,"has_more":false}}`)
				return 0
			case "query":
				mql := value("--mql")
				if strings.Contains(mql, ".`Sprint`") {
					fmt.Println(`{"data":{"1":[{"work_item_id":"900","name":"Fixture sprint","work_item_status":"进行中"}]}}`)
					return 0
				}
				rows := []map[string]string{}
				if strings.Contains(mql, "User Story") {
					for i := 0; i < max(1, state.ListSize); i++ {
						rows = append(rows, map[string]string{"work_item_id": strconv.Itoa(101 + i), "name": state.Title})
					}
				}
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"data": map[string]any{"1": rows}})
				return 0
			default:
				return 98
			}
		}
		if value("--fields") != "" {
			fmt.Println(`{"work_item_fields":[{"key":"duration","value":{"start_time":{"timestamp":1},"end_time":{"timestamp":999999}}}]}`)
			return 0
		}
		phase = "metadata"
	case "contact":
		phase = "contact-" + value("--query")
	case "im":
		if len(args) < 2 || args[1] != "+messages-send" || value("--as") != "user" || value("--profile") != profile || value("--idempotency-key") == "" {
			return 92
		}
		phase = "send-" + value("--user-id")
	case "query-tag":
		fmt.Println(`{"build_status":"成功","uat_status":"初始","production_status":"初始"}`)
		return 0
	default:
		fmt.Fprintln(os.Stderr, "unexpected fixture command", args[0])
		return 93
	}
	var number int
	for number = 1; ; number++ {
		marker := filepath.Join(state.Log, phase+"-"+strconv.Itoa(number)+".started")
		file, e := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(e, os.ErrExist) {
			continue
		}
		if e != nil {
			return 94
		}
		_, _ = fmt.Fprintln(file, value("--idempotency-key"))
		_ = file.Close()
		break
	}
	defer func() {
		_ = os.WriteFile(filepath.Join(state.Log, phase+"-"+strconv.Itoa(number)+".done"), nil, 0600)
	}()
	if state.Parallel {
		peer := ""
		switch phase {
		case "verify":
			if number == 1 {
				peer = "metadata"
			}
		case "metadata":
			if number == 1 {
				peer = "verify"
			}
		default:
			if strings.HasPrefix(phase, "contact-") && len(state.Testers) == 2 {
				peer = "contact-" + state.Testers[0]
				if phase == peer {
					peer = "contact-" + state.Testers[1]
				}
			}
		}
		if peer != "" {
			if err := waitNoticeFixtureFile(filepath.Join(state.Log, peer+"-"+strconv.Itoa(number)+".started")); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 95
			}
		}
	}
	if phase == state.GatePhase && state.Gate != "" {
		file, e := os.OpenFile(state.Gate+".claimed", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e == nil || state.GateAll {
			if file != nil {
				_ = file.Close()
			}
			_ = os.WriteFile(state.Gate+".started", nil, 0600)
			if waitNoticeFixtureFile(state.Gate+".release") != nil {
				return 96
			}
			state, err = readNoticeFixtureState(path)
			if err != nil {
				return 97
			}
		}
	}
	switch args[0] {
	case "config":
		profile := state.MeegleProfile
		if profile == "" {
			profile = "notice-fixture"
		}
		fmt.Println(profile)
	case "user":
		user := state.MeegleUser
		if user == "" {
			user = "notice-account"
		}
		fmt.Printf(`{"user_key":%q,"tenant_key":"notice-tenant"}`+"\n", user)
	case "auth":
		if phase == "meegleAuth" {
			fmt.Println(`{"authenticated":true,"host":"project.feishu.cn","tenant_key":"notice-tenant"}`)
			break
		}
		fmt.Printf(`{"appId":"cli_notice_fixture","identities":{"user":{"openId":%q,"userName":"Self","verified":%t,"tokenStatus":"valid","scope":"im:message im:message.send_as_user"}}}`+"\n", state.Sender, phase == "verify")
	case "workitem":
		if state.MetadataFailure {
			fmt.Fprintln(os.Stderr, "controlled metadata failure")
			return 1
		}
		if state.IncompleteRoster {
			fmt.Println(`{"name":"Incomplete","work_item_attribute":{"role_members":[{"name":"QC Owner","members":[{}]}]}}`)
			break
		}
		members := []map[string]string{}
		for _, name := range state.Testers {
			members = append(members, map[string]string{"name": name})
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"name": state.Title, "work_item_attribute": map[string]any{"role_members": []any{map[string]any{"name": "QC Owner", "members": members}}}})
	case "contact":
		name := value("--query")
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "data": map[string]any{"users": []any{map[string]any{"open_id": state.Contacts[name], "localized_name": name, "is_activated": true}}}})
	case "im":
		if state.ReadOnlyHome != "" {
			// No im command is ever delegated to the installed CLI. Keep an
			// explicit event so real read-only measurements cannot be confused
			// with a successful network message RPC.
			writeNoticeProbeEvent(state.Log, noticeProbeEvent{Phase: "message-rpc", StartedAt: time.Now().UTC().Format(time.RFC3339Nano), RPCMocked: true})
			fmt.Fprintln(os.Stderr, "RPC mocked: local success; no real message request")
		}
		switch state.Results[value("--user-id")] {
		case "unknown":
			fmt.Println("incomplete response")
		case "failed":
			fmt.Fprintln(os.Stderr, `{"ok":false,"error":{"code":123,"message":"fixture rejected"}}`)
			return 1
		default:
			fmt.Printf(`{"ok":true,"data":{"message_id":"om_%s"}}`+"\n", value("--user-id"))
		}
	}
	return 0
}

type tagNoticeDispatchFixture struct {
	app        *App
	scope      TagJobScope
	operations map[string]domain.TagOperation
	action     Action
	path       string
	state      noticeCLIFixtureState
	accountDir string
}

func newTagNoticeDispatchFixture(t *testing.T, testers ...string) *tagNoticeDispatchFixture {
	t.Helper()
	a, scope, operations := tagJobProbeFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	batch, now := "notice-fixture-batch", domain.FormatSilverWingTime(time.Now())
	for name, op := range operations {
		cfg.Repositories = append(cfg.Repositories, domain.RepositoryConfig{ID: name, Name: name, RootPath: filepath.Join(scope.TaskPath, name), GitCommonDirectory: filepath.Join(scope.TaskPath, name, ".git")})
		cfg.Groups[0].Services = append(cfg.Groups[0].Services, domain.GroupServiceConfig{ID: name, DisplayName: name, RepositoryID: name, GenbuServiceName: name, GenbuProbeEnabled: true, MasterBranch: "origin/master", TestTagBaselineRef: "origin/release/test", DevelopmentTool: domain.DevelopmentToolVisualStudioCode, Modules: []domain.ServiceModuleConfig{{ID: "module", Name: "default", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildCurrentBranch, TagEnabled: true}}})
		op.BatchID = &batch
		op.GenbuStatus = domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess, UAT: domain.GenbuInitial, Production: domain.GenbuInitial, CheckedAt: &now}
		operations[name] = op
		saveTagProbeFixture(t, scope.TaskPath, op)
	}
	cfg.LarkExecutablePath, cfg.MeegleExecutablePath, cfg.GenbuExecutablePath = &executable, &executable, &executable
	cfg.CommandProxyURL, cfg.CommandProxyTargets = nil, nil
	cfg.LarkNotice = &domain.LarkNoticeConfig{Profile: "notice-fixture", AppID: "cli_notice_fixture", Contacts: []domain.LarkNoticeContact{}}
	cfg.MeegleProjects = []domain.MeegleProjectConfig{{SimpleName: "notice", ProjectKey: "notice-project"}}
	if err := a.Config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	cfg, _ = a.Config.Load()
	entry, err := a.Manifests.LoadTask(scope.TaskRoot, scope.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	entry.Manifest.RequirementLink = "https://project.feishu.cn/notice/userstory/detail/101"
	if err := a.Manifests.Save(scope.TaskPath, entry.Manifest); err != nil {
		t.Fatal(err)
	}
	a.Runner = process.NewRunner()
	a.Integrations = integrations.NewService(a.Paths, a.Config, a.Runner)
	action := tagProbeFixtureAction(scope, "tag.notice.prepare", "")
	action.Payload["groupId"], action.Payload["noticeConfigIdentity"] = batch, ReleaseNoticeConfigIdentity(cfg)
	f := &tagNoticeDispatchFixture{app: a, scope: scope, operations: operations, action: action, path: filepath.Join(a.Paths.Home, "notice-fixture.json"), state: noticeCLIFixtureState{
		Log: filepath.Join(a.Paths.Home, "notice-cli-log"), Sender: "ou_self", Title: "Notice fixture", Testers: testers,
		Contacts: map[string]string{"TesterA": "ou_a", "TesterB": "ou_b", "TesterC": "ou_c"}, Results: map[string]string{},
	}}
	f.accountDir = filepath.Join(a.Paths.Home, "isolated-account")
	t.Setenv("USERPROFILE", f.accountDir)
	t.Setenv("HOME", f.accountDir)
	if err := os.MkdirAll(f.state.Log, 0700); err != nil {
		t.Fatal(err)
	}
	f.saveState(t)
	t.Setenv("SILVERWING_TAG_NOTICE_FIXTURE", f.path)
	t.Cleanup(func() { _ = a.Integrations.CloseRequirementDocuments(context.Background()) })
	return f
}

func (f *tagNoticeDispatchFixture) saveState(t *testing.T) {
	t.Helper()
	data, err := json.Marshal(f.state)
	if err == nil {
		err = os.WriteFile(f.path, data, 0600)
	}
	if err != nil {
		t.Fatal(err)
	}
	if f.accountDir != "" {
		profile, user := f.state.MeegleProfile, f.state.MeegleUser
		if profile == "" {
			profile = "notice-fixture"
		}
		if user == "" {
			user = "notice-account"
		}
		dir := filepath.Join(f.accountDir, ".meegle")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		config, _ := json.Marshal(map[string]any{"current": profile, "profiles": map[string]any{profile: map[string]string{"host": "project.feishu.cn"}}})
		for name, data := range map[string][]byte{"config.json": config, "credentials-" + profile + ".enc": []byte("fake encrypted credential for " + user)} {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func (f *tagNoticeDispatchFixture) prepare(t *testing.T) releasenotice.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := f.app.Dispatch(ctx, f.action)
	if err != nil {
		t.Fatal(err)
	}
	// Preserve the desktop's JSON boundary and prepared snapshot exactly.
	data, err := json.Marshal(result.Data)
	var session releasenotice.Session
	if err != nil || json.Unmarshal(data, &session) != nil {
		t.Fatal("invalid prepare response", err)
	}
	return session
}

func (f *tagNoticeDispatchFixture) sendAction(t *testing.T, saved releasenotice.Session) Action {
	t.Helper()
	choice, err := saved.SendChoice()
	if err != nil {
		t.Fatal(err)
	}
	action := f.action
	action.Kind, action.Payload = "tag.notice.send", map[string]any{}
	for key, value := range f.action.Payload {
		action.Payload[key] = value
	}
	action.Payload["confirmed"], action.Payload["sessionId"], action.Payload["selection"] = true, saved.ID, choice
	return action
}

func (f *tagNoticeDispatchFixture) count(t *testing.T, phase string) int {
	t.Helper()
	files, err := os.ReadDir(f.state.Log)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, file := range files {
		if strings.HasPrefix(file.Name(), phase+"-") && strings.HasSuffix(file.Name(), ".started") {
			n++
		}
	}
	return n
}

func (f *tagNoticeDispatchFixture) arm(t *testing.T, phase string) {
	t.Helper()
	f.state.GatePhase, f.state.Gate = phase, filepath.Join(f.app.Paths.Home, "notice-gate")
	f.saveState(t)
	t.Cleanup(func() { _ = os.WriteFile(f.state.Gate+".release", nil, 0600) })
}

func TestTagNoticeDispatchReducesSingleRecipientCallsAndExplicitResend(t *testing.T) {
	f := newTagNoticeDispatchFixture(t, "TesterA")
	f.state.Parallel = true
	f.saveState(t)
	keys := []string{}
	for click := 1; click <= 2; click++ {
		prepared := f.prepare(t)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		result, err := f.app.Dispatch(ctx, f.sendAction(t, prepared))
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		sent := result.Data.(releasenotice.Session)
		if sent.Deliveries()[0].State != "sent" || len(sent.Receipts) != click {
			t.Fatal(sent)
		}
		keys = append(keys, sent.Receipts[len(sent.Receipts)-1].Key)
		if f.count(t, "verify") != 2*click || f.count(t, "metadata") != 1 || f.count(t, "contact-TesterA") != 1 || f.count(t, "local") != 2*click || f.count(t, "send-ou_a") != click || f.count(t, "meegleUser") != 1 || f.count(t, "meegleAuth") != 1 || f.count(t, "meegleProfile") != 1 {
			t.Fatal("dispatch did not use optimized checks", click, f.count(t, "verify"), f.count(t, "metadata"), f.count(t, "contact-TesterA"), f.count(t, "local"))
		}
	}
	if keys[0] == keys[1] {
		t.Fatal("explicit desktop resend reused a successful key")
	}
	t.Log("desktop cold click: verify=2 metadata=1 directory=1 Meegle identity commands=3 local=2; next click: verify=2 metadata=0 directory=0 Meegle identity=0 local=2; cold identity/metadata barrier passed")
}

func TestTagNoticeDispatchParallelRosterAndPartialUnknownRetry(t *testing.T) {
	for _, outcome := range []string{"unknown", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			f := newTagNoticeDispatchFixture(t, "TesterA", "TesterB")
			f.state.Parallel, f.state.Results["ou_b"] = true, outcome
			f.saveState(t)
			prepared := f.prepare(t)
			result, err := f.app.Dispatch(context.Background(), f.sendAction(t, prepared))
			if err == nil {
				t.Fatal("partial failure reported success")
			}
			sent := result.Data.(releasenotice.Session)
			if got := sent.Deliveries(); got[0].State != "sent" || got[1].State != outcome {
				t.Fatal(got)
			}
			original := sent.Receipts[1]
			delete(f.state.Results, "ou_b")
			f.saveState(t)
			prepared = f.prepare(t)
			result, err = f.app.Dispatch(context.Background(), f.sendAction(t, prepared))
			if err != nil {
				t.Fatal(err)
			}
			sent = result.Data.(releasenotice.Session)
			if f.count(t, "send-ou_a") != 1 || f.count(t, "send-ou_b") != 2 || sent.Receipts[1].Key != original.Key || sent.Receipts[1].Card != original.Card {
				t.Fatal("partial retry duplicated success or changed original message", sent)
			}
			if f.count(t, "verify") != 5 || f.count(t, "metadata") != 1 || f.count(t, "contact-TesterA") != 1 || f.count(t, "contact-TesterB") != 1 {
				t.Fatal("multi-recipient checks were not scoped per actual RPC")
			}
			t.Log("identity/metadata and both directory lookups crossed parallel barriers; partial retry retained original key/card and skipped success")
		})
	}
}

func TestTagNoticeDispatchRejectsChangesDuringQueries(t *testing.T) {
	for _, mode := range []string{"account", "meegle-account", "meegle-profile", "profile", "config-contacts", "task-recreated", "task-link", "batch-tag", "batch-added", "genbu-failed", "genbu-stale", "roster", "contact", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := newTagNoticeDispatchFixture(t, "TesterA")
			prepared := f.prepare(t)
			phase := "verify"
			if mode == "account" {
				phase = "local"
			}
			f.arm(t, phase)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			done := make(chan error, 1)
			action := f.sendAction(t, prepared)
			go func() { _, err := f.app.Dispatch(ctx, action); done <- err }()
			if err := waitNoticeFixtureFile(f.state.Gate + ".started"); err != nil {
				t.Fatal(err)
			}
			if mode == "account" {
				if err := waitNoticeFixtureFile(filepath.Join(f.state.Log, "verify-2.done")); err != nil {
					t.Fatal(err)
				}
			}
			f.mutate(t, mode, cancel)
			if mode == "roster" || mode == "contact" {
				f.refresh(t)
			}
			if err := os.WriteFile(f.state.Gate+".release", nil, 0600); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil || f.count(t, "send") != 0 {
					t.Fatal("changed snapshot reached send", mode, err)
				}
				if mode == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation was swallowed", err)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("dispatch did not finish after barrier release/cancellation")
			}
		})
	}
}

func (f *tagNoticeDispatchFixture) mutate(t *testing.T, mode string, cancel context.CancelFunc) {
	t.Helper()
	switch mode {
	case "meegle-account":
		f.state.MeegleUser = "other-account"
		f.saveState(t)
	case "meegle-profile":
		f.state.MeegleProfile = "other-profile"
		f.saveState(t)
	case "account":
		f.state.Sender = "ou_other"
		f.saveState(t)
	case "roster":
		f.state.Testers = []string{"TesterC"}
		f.saveState(t)
	case "contact":
		f.state.Contacts["TesterA"] = "ou_c"
		f.saveState(t)
	case "profile", "config-contacts":
		config, err := f.app.Config.Load()
		if err != nil {
			t.Fatal(err)
		}
		if mode == "profile" {
			config.LarkNotice.Profile = "changed-profile"
		} else {
			config.LarkNotice.Contacts = []domain.LarkNoticeContact{{Name: "TesterA", OpenID: "ou_c"}}
		}
		if err := f.app.Config.Save(config); err != nil {
			t.Fatal(err)
		}
	case "task-recreated", "task-link":
		entry, err := f.app.Manifests.LoadTask(f.scope.TaskRoot, f.scope.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "task-recreated" {
			entry.Manifest.CreatedAt = "2026-11-10 01:02:03"
		} else {
			entry.Manifest.RequirementLink = "https://project.feishu.cn/notice/userstory/detail/202"
		}
		if err := f.app.Manifests.Save(f.scope.TaskPath, entry.Manifest); err != nil {
			t.Fatal(err)
		}
	case "batch-tag", "batch-added", "genbu-failed", "genbu-stale":
		op := f.operations["slow"]
		switch mode {
		case "batch-tag":
			tag := "changed.beta-2"
			op.Tag = &tag
		case "batch-added":
			op.OperationID = delivery.NewTagJobID()
		case "genbu-failed":
			op.GenbuStatus.Build = domain.GenbuFailed
		case "genbu-stale":
			stamp := domain.FormatSilverWingTime(time.Now().Add(-2 * time.Minute))
			op.GenbuStatus.CheckedAt = &stamp
		}
		saveTagProbeFixture(t, f.scope.TaskPath, op)
	case "cancel":
		cancel()
	default:
		t.Fatal("unknown mutation", mode)
	}
}

func TestTagNoticeDispatchStopsAfterChangesBetweenRecipients(t *testing.T) {
	for _, mode := range []string{"account", "profile", "roster", "task-recreated", "batch-tag", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := newTagNoticeDispatchFixture(t, "TesterA", "TesterB")
			prepared := f.prepare(t)
			f.arm(t, "send-ou_a")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			type answer struct {
				result ActionResult
				err    error
			}
			done := make(chan answer, 1)
			action := f.sendAction(t, prepared)
			go func() { result, err := f.app.Dispatch(ctx, action); done <- answer{result, err} }()
			if err := waitNoticeFixtureFile(f.state.Gate + ".started"); err != nil {
				t.Fatal(err)
			}
			f.mutate(t, mode, cancel)
			if mode == "roster" {
				f.refresh(t)
			}
			_ = os.WriteFile(f.state.Gate+".release", nil, 0600)
			got := <-done
			if got.err == nil || f.count(t, "send-ou_a") != 1 || f.count(t, "send-ou_b") != 0 {
				t.Fatal("later recipient reached send after change", got.err)
			}
			sent := got.result.Data.(releasenotice.Session)
			if sent.Deliveries()[1].State != "queued" || sent.Receipts[1].Key == "" {
				t.Fatal("unstarted recipient reservation lost", sent)
			}
		})
	}
}
