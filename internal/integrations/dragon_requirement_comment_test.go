package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

const (
	dragonCommentTestLink = "https://project.feishu.cn/fixture/userstory/detail/7140012345"
	dragonCommentTestID   = "13061"
	dragonCommentTestURL  = dragonCommentApprovalURLPrefix + dragonCommentTestID
	dragonCommentTestBody = "提测审批链接：" + dragonCommentTestURL + "\n" + dragonCommentNotice
)

func init() {
	root := os.Getenv("SW_DRAGON_COMMENT_HELPER")
	if root != "" {
		os.Exit(runDragonCommentFixture(root, os.Args[1:]))
	}
}

func runDragonCommentFixture(root string, args []string) int {
	write := func(name, value string) error {
		return os.WriteFile(filepath.Join(root, name), []byte(value), 0o600)
	}
	read := func(name string) string {
		data, _ := os.ReadFile(filepath.Join(root, name))
		return string(data)
	}
	appendCall := func(name string) error {
		log, err := os.OpenFile(filepath.Join(root, "calls"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer log.Close()
		_, err = fmt.Fprintln(log, name)
		return err
	}
	flag := func(name string) string {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == name {
				return args[i+1]
			}
		}
		return ""
	}
	emit := func(value any) int {
		if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
			return 2
		}
		return 0
	}
	row := func(id, content string) map[string]any {
		return map[string]any{"comment_id": id, "content": content}
	}
	page := func(number, total, pages int, rows ...map[string]any) int {
		comments := append([]map[string]any{}, rows...)
		return emit(map[string]any{"comments": comments, "pagination": map[string]any{
			"page_num": number, "total": total, "total_pages": pages, "has_more": number < pages,
		}})
	}
	if len(args) < 2 {
		return 2
	}
	operation := args[0] + " " + args[1]
	if flag("--work-item-id") != "" && flag("--work-item-id") != "7140012345" ||
		flag("--project-key") != "" && flag("--project-key") != "fixture-project" {
		return 2
	}
	mode := read("mode")
	switch operation {
	case "config profile":
		fmt.Println("fixture-profile")
		return 0
	case "auth status":
		return emit(map[string]any{"authenticated": true, "host": "project.feishu.cn", "tenant_key": "fixture"})
	case "user me":
		account := read("account")
		if account == "" {
			account = "account-a"
		}
		return emit(map[string]any{"user_key": account})
	case "project search":
		return emit(map[string]any{"projects": []map[string]string{{"project_key": "fixture-project", "simple_name": "fixture", "name": "Fixture"}}})
	case "workitem get":
		id := "7140012345"
		if mode == "wrong-workitem" {
			id = "7140012346"
		}
		return emit(map[string]string{"work_item_id": id, "project_key": "fixture-project"})
	case "comment list":
		if err := appendCall("list " + flag("--page-num")); err != nil {
			return 2
		}
		switch mode {
		case "contradictory-pagination":
			return emit(map[string]any{
				"comments":   []map[string]any{row("100", dragonCommentNotice)},
				"pagination": map[string]any{"page_num": 1, "total_pages": 2, "has_more": false},
			})
		case "existing-page-2", "failed-page-2":
			if flag("--page-num") == "1" {
				return page(1, 2, 2, row("100", "提测审批链接："+dragonCommentApprovalURLPrefix+"130610\n"+dragonCommentNotice))
			}
			if mode == "failed-page-2" {
				return 3
			}
			return page(2, 2, 2, row("101", `<p>提测审批链接：<a href="`+dragonCommentTestURL+`">查看审批</a></p>`))
		case "failed-readback":
			if read("attempt") != "" {
				return 3
			}
		}
		if posted := read("posted"); posted != "" {
			return page(1, 1, 1, row("200", posted))
		}
		return page(1, 0, 1)
	case "comment add":
		raw, err := os.ReadFile(strings.TrimPrefix(flag("--params"), "@"))
		if err != nil || flag("--action") != "create" || flag("--comment-id") != "" {
			return 2
		}
		var params map[string]string
		if json.Unmarshal(raw, &params) != nil || params["content"] != dragonCommentTestBody {
			return 2
		}
		if args[len(args)-1] == "--dry-run" {
			if err := appendCall("dry-run"); err != nil {
				return 2
			}
			params["project_key"] = flag("--project-key")
			params["work_item_id"] = flag("--work-item-id")
			params["action"] = flag("--action")
			if mode == "wrong-dry-run" {
				params["work_item_id"] = "7140012346"
			}
			if mode == "change-account" && write("account", "account-b") != nil {
				return 2
			}
			return emit(map[string]any{"tool": "add_comment", "dry_run": true, "params": params})
		}
		if appendCall("add") != nil || write("attempt", "true") != nil {
			return 2
		}
		if mode == "error-envelope" {
			return emit(map[string]any{"code": 1001, "message": "fixture rejection"})
		}
		content := params["content"]
		if mode == "wrong-readback-body" {
			content = dragonCommentTestURL
		}
		if mode != "unknown-absent" && write("posted", content) != nil {
			return 2
		}
		if mode == "unknown-absent" || mode == "unknown-written" {
			return 3
		}
		if mode == "wrong-readback-id" {
			return emit(map[string]any{"comment_id": "201"})
		}
		return emit(map[string]any{"comment_id": "200"})
	default:
		return 2
	}
}

func TestDragonRequirementCommentReference(t *testing.T) {
	tests := []struct {
		name, content string
		match         bool
	}{
		{"canonical plain", dragonCommentTestBody, true},
		{"Markdown preview", "[审批详情](" + dragonCommentTestURL + ")<!-- linkPreview -->", true},
		{"HTML link", `<a href="` + dragonCommentTestURL + `">审批详情</a>`, true},
		{"HTML numeric entity in href", `<a href="https://dragon.snowballtech.com/&#35;/workorder/detail/13061">审批详情</a>`, true},
		{"explicit ID", "青龙提测审批 **ID：13061**", true},
		{"explicit number", "青龙审批编号：**13061**", true},
		{"explicit field", "approval_id=13061", true},
		{"explicit English ID", "Dragon approval #13061", true},
		{"same words", dragonCommentNotice, false},
		{"bare number", "当前是 13061", false},
		{"longer ID", "[审批详情](" + dragonCommentTestURL + "0)", false},
		{"noncanonical route extension", "[审批详情](" + dragonCommentTestURL + "/status)", false},
		{"noncanonical parameters", "[审批详情](" + dragonCommentTestURL + "?x=y)", false},
		{"another site's query parameter", "https://example.test/?next=" + dragonCommentTestURL, false},
		{"numeric prefix", "青龙审批 ID：130610", false},
		{"identifier prefix", "青龙审批 ID：13061a", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comments := []domain.RequirementComment{{ID: "1", Content: tt.content}}
			if matched := findDragonRequirementComment(comments, dragonCommentTestID, dragonCommentTestURL, "", false) != ""; matched != tt.match {
				t.Fatalf("match = %v, want %v for %q", matched, tt.match, tt.content)
			}
		})
	}
}

func TestDragonRequirementCommentSync(t *testing.T) {
	tests := []struct {
		name, mode, expectedID, expectedCalls string
		failed, uncertain                     bool
	}{
		{"add then dedup", "", "200", "list 1\ndry-run\nadd\nlist 1\nlist 1\n", false, false},
		{"dedup on page two", "existing-page-2", "101", "list 1\nlist 2\n", false, false},
		{"abort after failed page", "failed-page-2", "", "list 1\nlist 2\n", true, false},
		{"abort after contradictory pagination", "contradictory-pagination", "", "list 1\n", true, false},
		{"block wrong target", "wrong-workitem", "", "", true, false},
		{"block unexpected dry run target", "wrong-dry-run", "", "list 1\ndry-run\n", true, false},
		{"block account switch before write", "change-account", "", "list 1\ndry-run\n", true, false},
		{"confirm failed add via readback", "unknown-written", "200", "list 1\ndry-run\nadd\nlist 1\n", false, false},
		{"ambiguous add with failed readback", "failed-readback", "", "list 1\ndry-run\nadd\nlist 1\n", true, true},
		{"ambiguous add without comment", "unknown-absent", "", "list 1\ndry-run\nadd\nlist 1\n", true, true},
		{"error envelope without confirmed outcome", "error-envelope", "", "list 1\ndry-run\nadd\nlist 1\n", true, true},
		{"wrong readback body", "wrong-readback-body", "", "list 1\ndry-run\nadd\nlist 1\n", true, true},
		{"wrong readback receipt ID", "wrong-readback-id", "", "list 1\ndry-run\nadd\nlist 1\n", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, root, scope := newDragonCommentTestService(t, tt.mode)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			id, err := service.SyncDragonRequirementComment(ctx, dragonCommentTestLink, dragonCommentTestID, dragonCommentTestURL, scope)
			var uncertain *DragonRequirementCommentUncertainError
			if id != tt.expectedID || (err != nil) != tt.failed || errors.As(err, &uncertain) != tt.uncertain {
				t.Fatalf("id=%q err=%v, want id=%q failed=%v uncertain=%v", id, err, tt.expectedID, tt.failed, tt.uncertain)
			}
			if tt.mode == "" {
				id, err = service.SyncDragonRequirementComment(ctx, dragonCommentTestLink, dragonCommentTestID, dragonCommentTestURL, scope)
				if id != "200" || err != nil {
					t.Fatalf("dedup retry = %q, %v", id, err)
				}
			}
			assertDragonCommentFixtureCalls(t, root, tt.expectedCalls)
		})
	}
}

func TestDragonRequirementCommentReadOnlyRecoveryAndScope(t *testing.T) {
	service, root, scope := newDragonCommentTestService(t, "unknown-absent")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := service.CheckDragonRequirementComment(ctx, dragonCommentTestLink, dragonCommentTestID, dragonCommentTestURL, scope)
	if id != "" || err != nil {
		t.Fatalf("absent = %q, %v", id, err)
	}
	if err := os.WriteFile(filepath.Join(root, "posted"), []byte(dragonCommentTestBody), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err = service.CheckDragonRequirementComment(ctx, dragonCommentTestLink, dragonCommentTestID, dragonCommentTestURL, scope)
	if id != "200" || err != nil {
		t.Fatalf("confirmed = %q, %v", id, err)
	}
	if err := os.WriteFile(filepath.Join(root, "account"), []byte("account-b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CheckDragonRequirementComment(ctx, dragonCommentTestLink, dragonCommentTestID, dragonCommentTestURL, scope); err == nil {
		t.Fatal("different account was allowed to confirm")
	}
	if _, err := service.SyncDragonRequirementComment(ctx, dragonCommentTestLink, dragonCommentTestID, dragonCommentTestURL, scope); err == nil {
		t.Fatal("different account was allowed to sync")
	}
	assertDragonCommentFixtureCalls(t, root, "list 1\nlist 1\n")
}

func TestDragonRequirementCommentStopsAfterAdmissionChanges(t *testing.T) {
	for _, tt := range []struct {
		name, stopAfter, calls string
		uncertain              bool
	}{
		{"before write", "dry-run\n", "list 1\ndry-run\n", false},
		{"after write", "\nadd\n", "list 1\ndry-run\nadd\n", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service, root, scope := newDragonCommentTestService(t, "")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			blocked := errors.New("Dragon disabled")
			ctx = commandruntime.WithAdmission(ctx, func(context.Context) error {
				calls, err := os.ReadFile(filepath.Join(root, "calls"))
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if strings.Contains(string(calls), tt.stopAfter) {
					return blocked
				}
				return nil
			})
			id, err := service.SyncDragonRequirementComment(ctx, dragonCommentTestLink, dragonCommentTestID, dragonCommentTestURL, scope)
			var uncertain *DragonRequirementCommentUncertainError
			if id != "" || !errors.Is(err, blocked) || errors.As(err, &uncertain) != tt.uncertain {
				t.Fatalf("id=%q err=%v, want admission error with uncertain=%v", id, err, tt.uncertain)
			}
			assertDragonCommentFixtureCalls(t, root, tt.calls)
		})
	}
}

func newDragonCommentTestService(t *testing.T, mode string) (*Service, string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SW_DRAGON_COMMENT_HELPER", root)
	t.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	if err := os.WriteFile(filepath.Join(root, "mode"), []byte(mode), 0o600); err != nil {
		t.Fatal(err)
	}
	cli, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	paths := storage.NewPaths(filepath.Join(root, "home"))
	store := storage.NewConfigStore(paths)
	config := domain.DefaultAppConfig()
	config.MeegleExecutablePath = &cli
	config.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "fixture-project", SimpleName: "fixture"}}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	service := NewService(paths, store, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	scope, err := service.ResolveDragonRequirementCommentScope(ctx, dragonCommentTestLink)
	if err != nil || scope == "" {
		t.Fatalf("resolve scope = %q, %v", scope, err)
	}
	return service, root, scope
}

func assertDragonCommentFixtureCalls(t *testing.T, root, expected string) {
	t.Helper()
	calls, err := os.ReadFile(filepath.Join(root, "calls"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if string(calls) != expected {
		t.Fatalf("CLI calls = %q, want %q", calls, expected)
	}
}
