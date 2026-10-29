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

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

// The test executable is the controlled CLI. No installed CLI, account or
// network is accessed, and all mutable fixture state lives under TempDir.
func init() {
	root := os.Getenv("SW_REQUIREMENT_READ_HELPER")
	if root == "" {
		return
	}
	args := os.Args[1:]
	encoded, _ := json.Marshal(args)
	log, err := os.OpenFile(filepath.Join(root, "calls.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(2)
	}
	_, _ = log.Write(append(encoded, '\n'))
	_ = log.Close()
	account, _ := os.ReadFile(filepath.Join(root, "account"))
	if len(account) == 0 {
		account = []byte("account-a")
	}
	mode, _ := os.ReadFile(filepath.Join(root, "mode"))
	flag := func(name string) string {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == name {
				return args[i+1]
			}
		}
		return ""
	}
	emit := func(value any) { _ = json.NewEncoder(os.Stdout).Encode(value); os.Exit(0) }
	if len(args) < 2 {
		os.Exit(2)
	}
	switch args[0] + " " + args[1] {
	case "config profile":
		fmt.Println("fixture-profile")
		os.Exit(0)
	case "auth status":
		emit(map[string]any{"authenticated": true, "host": "project.feishu.cn", "tenant_key": "fixture-tenant"})
	case "user me":
		emit(map[string]any{"user_key": string(account), "tenant_key": "fixture-tenant"})
	case "workitem get":
		if string(mode) == "fail-workitem" {
			os.Exit(3)
		}
		emit(map[string]any{"name": "Fixture requirement", "description": "fixture body", "work_item_attribute": map[string]any{"name": "Fixture requirement", "work_item_status": map[string]any{"name": "In Progress"}, "role_members": []any{}}})
	case "comment list":
		emit(map[string]any{"comments": []any{}, "pagination": map[string]any{"page_num": 1, "total": 0, "total_pages": 1, "has_more": false}})
	case "attachment +download":
		if string(mode) == "switch-on-download" {
			_ = os.WriteFile(filepath.Join(root, "account"), []byte("account-b"), 0600)
		}
		if err := os.WriteFile(flag("--output"), []byte{137, 80, 78, 71, 13, 10, 26, 10, 1}, 0600); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(2)
}

type requirementReadFixture struct {
	service    *Service
	config     domain.AppConfig
	root       string
	item       domain.ParticipatedWorkItem
	token, raw string
}

func newRequirementReadFixture(t *testing.T) requirementReadFixture {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SW_REQUIREMENT_READ_HELPER", root)
	t.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	cli, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	paths := storage.NewPaths(filepath.Join(root, "home"))
	store := storage.NewConfigStore(paths)
	config := domain.DefaultAppConfig()
	config.MeegleExecutablePath = &cli
	config.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "project-a", SimpleName: "space"}}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	config, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(paths, store, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, _, err := service.requirementIdentityFor(ctx, config, "", true, service.requirementCacheIdentity)
	if err != nil {
		t.Fatal(err)
	}
	return requirementReadFixture{service: service, config: config, root: root, token: session.token, raw: session.raw,
		item: domain.ParticipatedWorkItem{ProjectKey: "project-a", ProjectName: "space", Type: "userstory", ID: "123", URL: "https://project.feishu.cn/space/userstory/detail/123"}}
}

func (f requirementReadFixture) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.root, "calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var args []string
		if err := json.Unmarshal([]byte(line), &args); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, strings.Join(args, " "))
	}
	return calls
}

func (f requirementReadFixture) payload() map[string]any {
	return map[string]any{"url": f.item.URL, "projectKey": f.item.ProjectKey, "expectedIdentity": f.token}
}

func TestRequirementImageEntryReusesProbeForTwentyColdCommentImages(t *testing.T) {
	f := newRequirementReadFixture(t)
	comments := domain.RequirementComments{}
	for i := 0; i < 20; i++ {
		url := fmt.Sprintf("%s-%d", commentImageFixtureURL, i)
		comments.Comments = append(comments.Comments, domain.RequirementComment{ID: fmt.Sprintf("comment-%d", i), Content: "![](" + url + ")"})
	}
	key := requirementCacheKey("comments", f.raw, f.item)
	if _, err := f.service.writeRequirementCommentsCache(key, comments, 1); err != nil {
		t.Fatal(err)
	}
	before := len(f.calls(t))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	resources := f.service.requirementCommentsResult(key, RequirementCommentsReadResult{Comments: comments.Comments}).CommentResources
	if len(resources) != 20 {
		t.Fatalf("fixture resources=%d", len(resources))
	}
	for _, resource := range resources {
		payload := f.payload()
		payload["scope"], payload["commentID"], payload["commentGeneration"], payload["fileUrl"] = "comment", resource.CommentID, resource.Generation, resource.URL
		value, err := f.service.downloadRequirementAttachment(ctx, f.config, payload)
		if err != nil {
			t.Fatal(err)
		}
		if image, ok := value.(RequirementCommentResource); !ok || image.Path == "" || image.Error != "" {
			t.Fatalf("cold image failed: %T %+v", value, value)
		}
	}
	calls := f.calls(t)[before:]
	counts := map[string]int{}
	for _, call := range calls {
		for _, prefix := range []string{"config profile ", "auth status ", "user me ", "attachment +download "} {
			if strings.HasPrefix(call, prefix) {
				counts[prefix]++
			}
		}
	}
	if len(calls) != 140 || counts["config profile "] != 40 || counts["auth status "] != 40 || counts["user me "] != 40 || counts["attachment +download "] != 20 {
		t.Fatalf("expected one entry and one forced post-download probe per image (7 CLI calls): counts=%v calls=%d", counts, len(calls))
	}
}

func TestRequirementImagePostDownloadAccountSwitchRejectsPersistence(t *testing.T) {
	f := newRequirementReadFixture(t)
	content := "![](" + commentImageFixtureURL + ")"
	key := requirementCacheKey("comments", f.raw, f.item)
	comments := domain.RequirementComments{Comments: []domain.RequirementComment{{ID: "comment", Content: content}}}
	if _, err := f.service.writeRequirementCommentsCache(key, comments, 1); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "mode"), []byte("switch-on-download"), 0600); err != nil {
		t.Fatal(err)
	}
	payload := f.payload()
	payload["scope"], payload["commentID"], payload["commentGeneration"], payload["fileUrl"] = "comment", "comment", requirementCacheDigest(content), commentImageFixtureURL
	_, err := f.service.downloadRequirementAttachment(context.Background(), f.config, payload)
	var changed *RequirementIdentityChangedError
	if !errors.As(err, &changed) {
		t.Fatalf("account changed during download was accepted: %v", err)
	}
	resource, err := f.service.cachedCommentImageResource(key, "comment", requirementCacheDigest(content), commentImageFixtureURL)
	if err != nil || resource.Path != "" || resource.Error != "" {
		t.Fatalf("cross-account download was persisted: %+v %v", resource, err)
	}
}

func TestRequirementReadOperationReusesOnlyForcedPostReadProbe(t *testing.T) {
	f := newRequirementReadFixture(t)
	before := len(f.calls(t))
	ctx := WithRequirementReadOperation(context.Background())
	payload := f.payload()
	payload["force"], payload["fullRefresh"] = true, true
	for _, kind := range []string{"requirements.metadata", "requirements.read", "requirements.comments"} {
		if _, err := f.service.Execute(ctx, kind, payload); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	calls := f.calls(t)[before:]
	identityCalls := 0
	for _, call := range calls {
		if strings.HasPrefix(call, "config profile ") || strings.HasPrefix(call, "auth status ") || strings.HasPrefix(call, "user me ") {
			identityCalls++
		}
	}
	if identityCalls != 12 || len(calls) != 15 {
		t.Fatalf("three-stage refresh must share adjacent verified boundaries: identity=%d all=%v", identityCalls, calls)
	}
	// An unrelated new operation must make its own forced starting probe.
	before = len(f.calls(t))
	if _, err := f.service.Execute(WithRequirementReadOperation(context.Background()), "requirements.metadata", payload); err != nil {
		t.Fatal(err)
	}
	if got := len(f.calls(t)) - before; got != 7 {
		t.Fatalf("operation identity escaped its lifetime: calls=%d", got)
	}
}

func TestRequirementReadOperationRejectsChangedEpochAndRetiresOnFailure(t *testing.T) {
	for _, mode := range []string{"account", "config", "failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := newRequirementReadFixture(t)
			owner, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := WithRequirementReadOperation(owner)
			payload := f.payload()
			payload["force"], payload["fullRefresh"] = true, true
			if _, err := f.service.Execute(ctx, "requirements.metadata", payload); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "account":
				if err := os.WriteFile(filepath.Join(f.root, "account"), []byte("account-b"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, _, err := f.service.RequirementIdentity(context.Background(), f.config); err != nil {
					t.Fatal(err)
				}
			case "config":
				changed := f.config
				proxy := "http://127.0.0.1:23456"
				changed.CommandProxyURL = &proxy
				if err := f.service.config.Save(changed); err != nil {
					t.Fatal(err)
				}
			case "failure":
				finishRequirementReadOperation(ctx, RequirementMetadataReadResult{EstimateError: "fixture"}, nil)
				before := len(f.calls(t))
				if _, err := f.service.Execute(ctx, "requirements.metadata", payload); err != nil {
					t.Fatal(err)
				}
				if got := len(f.calls(t)) - before; got != 7 {
					t.Fatalf("failed step supplied next entry identity: %d", got)
				}
				return
			case "cancel":
				cancel()
			}
			before := len(f.calls(t))
			if _, err := f.service.Execute(ctx, "requirements.read", payload); err == nil {
				t.Fatal("stale/canceled operation continued reading")
			}
			if got := len(f.calls(t)) - before; got != 0 {
				t.Fatalf("stale operation invoked CLI: %d", got)
			}
		})
	}
}
