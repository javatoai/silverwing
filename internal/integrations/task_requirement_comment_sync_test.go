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

const (
	taskCommentFixtureLink = "https://project.feishu.cn/fixture/userstory/detail/7150074042"
	taskCommentFixtureBody = "服务与分支\napi-service：feature/development\n\n本地任务资料目录\nD:\\资料\\任务"
)

type taskCommentFixtureRow struct {
	ID        string `json:"comment_id"`
	Body      string `json:"content"`
	Author    string `json:"creator"`
	CreatedAt string `json:"created_at"`
}

func init() {
	if root := os.Getenv("SILVERWING_TASK_COMMENT_HELPER"); root != "" {
		os.Exit(runTaskCommentFixture(root, os.Args[1:]))
	}
}

func runTaskCommentFixture(root string, args []string) int {
	if len(args) < 2 {
		return 2
	}
	read := func(name string) []byte {
		value, _ := os.ReadFile(filepath.Join(root, name))
		return value
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
	key, id := flag("--project-key"), flag("--work-item-id")
	if key != "" && key != "fixture-project" || id != "" && id != "7150074042" {
		return 2
	}
	author := string(read("account"))
	if author == "" {
		author = "account-a"
	}
	comments := []taskCommentFixtureRow{}
	if raw := read("comments"); len(raw) > 0 && json.Unmarshal(raw, &comments) != nil {
		return 2
	}
	mode := string(read("mode"))
	switch args[0] + " " + args[1] {
	case "config profile":
		fmt.Println("fixture-profile")
		return 0
	case "auth status":
		return emit(map[string]any{"authenticated": true, "host": "project.feishu.cn", "tenant_key": "test"})
	case "user me":
		return emit(map[string]string{"user_key": author})
	case "project search":
		return emit(map[string]any{"projects": []map[string]string{{"project_key": "fixture-project", "simple_name": "fixture", "name": "Fixture"}}})
	case "workitem get":
		return emit(map[string]string{"work_item_id": id, "project_key": key})
	case "comment list":
		if (mode == "readback-unavailable" || mode == "lost-receipt") && len(read("written")) > 0 {
			return 3
		}
		return emit(map[string]any{"comments": comments, "pagination": map[string]any{"page_num": 1, "total": len(comments), "has_more": false}})
	case "comment add":
		var params map[string]string
		raw, err := os.ReadFile(strings.TrimPrefix(flag("--params"), "@"))
		if err != nil || json.Unmarshal(raw, &params) != nil || key == "" || id == "" {
			return 2
		}
		action, commentID := flag("--action"), flag("--comment-id")
		body := params["content"]
		if args[len(args)-1] == "--dry-run" {
			params["project_key"], params["work_item_id"], params["action"] = key, id, action
			if commentID != "" {
				params["comment_id"] = commentID
			}
			return emit(map[string]any{"tool": "add_comment", "dry_run": true, "params": params})
		}
		var intent TaskRequirementCommentAttempt
		if json.Unmarshal(read("intent"), &intent) != nil || intent.CommentID != commentID || intent.Body != body || intent.StartedAt == "" {
			return 2
		}
		if mode == "no-comment" {
			return 3
		}
		if stored := read("stored-body"); len(stored) != 0 {
			body = string(stored)
		}
		switch action {
		case "create":
			commentID = fmt.Sprint(len(comments) + 1)
			comments = append(comments, taskCommentFixtureRow{commentID, body, author, time.Now().UTC().Format(time.RFC3339)})
		case "update":
			found := false
			for i := range comments {
				if comments[i].ID == commentID && comments[i].Author == author {
					comments[i].Body, found = body, true
				}
			}
			if !found {
				return 3
			}
		default:
			return 2
		}
		data, err := json.Marshal(comments)
		if err != nil || os.WriteFile(filepath.Join(root, "comments"), data, 0o600) != nil {
			return 2
		}
		if os.WriteFile(filepath.Join(root, "written"), []byte(commentID), 0o600) != nil {
			return 2
		}
		if mode == "lost-receipt" {
			return 3
		}
		return emit(map[string]string{"comment_id": commentID})
	}
	return 2
}

func newTaskCommentFixture(t *testing.T) (*Service, string, TaskRequirementCommentScope) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SILVERWING_TASK_COMMENT_HELPER", root)
	t.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	for _, name := range []string{"TMPDIR", "TEMP", "TMP"} {
		t.Setenv(name, root)
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
	if err = store.Save(config); err != nil {
		t.Fatal(err)
	}
	service := NewService(paths, store, nil)
	scope, err := service.ResolveTaskRequirementCommentScope(context.Background(), taskCommentFixtureLink)
	if err != nil || scope.Account == "" || scope.AuthorKey != "account-a" {
		t.Fatalf("resolve task comment scope = %+v, %v", scope, err)
	}
	return service, root, scope
}

func persistTaskCommentFixture(root string, latest *TaskRequirementCommentAttempt) func(TaskRequirementCommentAttempt) error {
	return func(attempt TaskRequirementCommentAttempt) error {
		*latest = attempt
		data, err := json.Marshal(attempt)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, "intent"), data, 0o600)
	}
}

func writeTaskCommentFixtureFile(t *testing.T, root, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTaskRequirementCommentTransportKeepsIndependentCommentsAndUpdatesByID(t *testing.T) {
	service, root, scope := newTaskCommentFixture(t)
	var attempt TaskRequirementCommentAttempt
	persist := persistTaskCommentFixture(root, &attempt)
	guard := func() error { return nil }
	ctx := context.Background()
	req := TaskRequirementCommentWrite{Body: taskCommentFixtureBody}
	for _, expected := range []string{"1", "2"} {
		id, err := service.SyncTaskRequirementComment(ctx, taskCommentFixtureLink, scope, req, persist, guard)
		if err != nil || id != expected || attempt.ReceiptID != expected {
			t.Fatalf("create = %q, %v, intent %+v; want ID %q", id, err, attempt, expected)
		}
	}
	update := TaskRequirementCommentWrite{CommentID: "2", PreviousBody: req.Body, Body: req.Body + "-更新"}
	id, err := service.SyncTaskRequirementComment(ctx, taskCommentFixtureLink, scope, update, persist, guard)
	if err != nil || id != "2" {
		t.Fatalf("update = %q, %v", id, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "comments"))
	var comments []taskCommentFixtureRow
	if err != nil || json.Unmarshal(data, &comments) != nil || len(comments) != 2 || comments[0].Body != req.Body || comments[1].Body != update.Body {
		t.Fatalf("comments after update = %+v, %v", comments, err)
	}
	comments[1].Body = "用户另外修改了评论"
	edited, _ := json.Marshal(comments)
	for _, conflict := range []struct{ file, value string }{{"account", "account-b"}, {"comments", string(edited)}, {"comments", `[]`}} {
		writeTaskCommentFixtureFile(t, root, conflict.file, conflict.value)
		updated := TaskRequirementCommentWrite{CommentID: "2", PreviousBody: update.Body, Body: update.Body + "-新版"}
		_, err = service.SyncTaskRequirementComment(ctx, taskCommentFixtureLink, scope, updated, func(TaskRequirementCommentAttempt) error {
			t.Error("conflict reached write admission")
			return errors.New("conflict")
		}, guard)
		var blocked *TaskRequirementCommentConflictError
		if !errors.As(err, &blocked) {
			t.Fatalf("conflict for %s = %v", conflict.file, err)
		}
		writeTaskCommentFixtureFile(t, root, "account", "account-a")
	}
}

func TestTaskRequirementCommentTransportRechecksUncertainResultOnly(t *testing.T) {
	for _, fixture := range []struct{ mode, receipt string }{{"readback-unavailable", "2"}, {"lost-receipt", ""}} {
		t.Run(fixture.mode, func(t *testing.T) {
			service, root, scope := newTaskCommentFixture(t)
			before, err := json.Marshal([]taskCommentFixtureRow{{"1", taskCommentFixtureBody, scope.AuthorKey, time.Now().UTC().Format(time.RFC3339)}})
			if err != nil {
				t.Fatal(err)
			}
			writeTaskCommentFixtureFile(t, root, "comments", string(before))
			writeTaskCommentFixtureFile(t, root, "mode", fixture.mode)
			ctx := context.Background()
			var attempt TaskRequirementCommentAttempt
			_, err = service.SyncTaskRequirementComment(ctx, taskCommentFixtureLink, scope, TaskRequirementCommentWrite{Body: taskCommentFixtureBody}, persistTaskCommentFixture(root, &attempt), func() error { return nil })
			var pending *TaskRequirementCommentUncertainError
			if !errors.As(err, &pending) || attempt.ReceiptID != fixture.receipt {
				t.Fatalf("write without readback = %v, intent %+v", err, attempt)
			}
			writeTaskCommentFixtureFile(t, root, "mode", "")
			reopened := NewService(service.paths, service.config, nil)
			id, err := reopened.CheckTaskRequirementComment(ctx, taskCommentFixtureLink, scope, attempt)
			if err != nil || id != "2" {
				t.Fatalf("reopen and check = %q, %v", id, err)
			}
			data, err := os.ReadFile(filepath.Join(root, "comments"))
			var comments []taskCommentFixtureRow
			if err != nil || json.Unmarshal(data, &comments) != nil || len(comments) != 2 || comments[1].Body != taskCommentFixtureBody {
				t.Fatalf("check modified comments = %+v, %v", comments, err)
			}
		})
	}
}

func TestTaskRequirementCommentTransportReconcilesFeishuFenceFormatting(t *testing.T) {
	service, root, scope := newTaskCommentFixture(t)
	ctx := context.Background()
	guard := func() error { return nil }
	const original = "**服务与分支**\n```\napi-service：feature/dev\n```\n\n**本地任务资料目录**\n```\nD:\\资料\\任务\n```"
	const stored = "**服务与分支**\n\n```text\napi-service：feature/dev\n```\n**本地任务资料目录**\n```text\nD:\\资料\\任务\n```\n"
	writeTaskCommentFixtureFile(t, root, "mode", "lost-receipt")
	writeTaskCommentFixtureFile(t, root, "stored-body", stored)
	var attempt TaskRequirementCommentAttempt
	persist := persistTaskCommentFixture(root, &attempt)
	_, err := service.SyncTaskRequirementComment(ctx, taskCommentFixtureLink, scope, TaskRequirementCommentWrite{Body: original}, persist, guard)
	var pending *TaskRequirementCommentUncertainError
	if !errors.As(err, &pending) || attempt.ReceiptID != "" || attempt.SendToken == "" {
		t.Fatalf("create without readback = %v, intent %+v", err, attempt)
	}
	writeTaskCommentFixtureFile(t, root, "mode", "")
	reopened := NewService(service.paths, service.config, nil)
	id, err := reopened.CheckTaskRequirementComment(ctx, taskCommentFixtureLink, scope, attempt)
	if err != nil || id != "1" {
		t.Fatalf("check existing Feishu comment = %q, %v", id, err)
	}
	update := TaskRequirementCommentWrite{CommentID: id, PreviousBody: original, Body: strings.Replace(original, "feature/dev", "feature/update", 1) + "\n\n-来自Silverwing"}
	storedUpdate := strings.Replace(stored, "feature/dev", "feature/update", 1) + "-来自Silverwing\n"
	writeTaskCommentFixtureFile(t, root, "stored-body", storedUpdate)
	id, err = reopened.SyncTaskRequirementComment(ctx, taskCommentFixtureLink, scope, update, persist, guard)
	if err != nil || id != "1" {
		t.Fatalf("update Feishu comment with existing formatting = %q, %v", id, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "comments"))
	var comments []taskCommentFixtureRow
	if err != nil || json.Unmarshal(data, &comments) != nil || len(comments) != 1 || comments[0].Body != storedUpdate {
		t.Fatalf("check and update should keep one remote comment with upstream formatting: %+v, %v", comments, err)
	}
}

func TestMatchTaskRequirementCommentChecksTextInsideFencesAndFooter(t *testing.T) {
	const original = "**服务与分支**\n```\napi-service：feature/dev\n\nworker：feature/dev\n```\n\n-来自Silverwing"
	const stored = "**服务与分支**\n\n```text\napi-service：feature/dev\n\nworker：feature/dev\n```\n-来自Silverwing\n"
	attempt := TaskRequirementCommentAttempt{Body: original, ReceiptID: "1", StartedAt: time.Now().UTC().Format(time.RFC3339)}
	comments := []domain.RequirementComment{{ID: "1", AuthorKey: "account-a", Content: stored}}
	if id, err := matchTaskRequirementComment(comments, "account-a", attempt); err != nil || id != "1" || comments[0].Content != stored {
		t.Fatalf("match = %q, %v; cached body = %q", id, err, comments[0].Content)
	}
	for _, body := range []string{
		strings.Replace(stored, "\n\nworker", "\nworker", 1),
		strings.Replace(stored, "feature/dev", "feature/other", 1),
		strings.Replace(stored, "```text", "```bash", 1),
		strings.Replace(stored, "-来自Silverwing", "-来自其他工具", 1),
		strings.Replace(stored, "-来自Silverwing\n", "", 1),
	} {
		comments[0].Content = body
		if id, err := matchTaskRequirementComment(comments, "account-a", attempt); err == nil || id != "" {
			t.Fatalf("matched different comment %q: %q, %v", body, id, err)
		}
	}
}

func TestTaskRequirementCommentTransportStopsIfTaskChangesAtAdmission(t *testing.T) {
	service, root, scope := newTaskCommentFixture(t)
	var attempt TaskRequirementCommentAttempt
	_, err := service.SyncTaskRequirementComment(context.Background(), taskCommentFixtureLink, scope, TaskRequirementCommentWrite{Body: taskCommentFixtureBody}, persistTaskCommentFixture(root, &attempt), func() error {
		if attempt.StartedAt != "" {
			return errors.New("task changed after saving intent")
		}
		return nil
	})
	var notSent interface{ RequestNotSent() }
	if !errors.As(err, &notSent) || attempt.StartedAt == "" {
		t.Fatalf("write admission did not provide the cancellation after saved intent: %+v, %v", attempt, err)
	}
	if _, err := os.Stat(filepath.Join(root, "written")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("write was attempted after task change: %v", err)
	}
}

func TestTaskRequirementCommentTransportDoesNotAdoptAnotherProfilesComment(t *testing.T) {
	service, root, scope := newTaskCommentFixture(t)
	ctx := context.Background()
	guard := func() error { return nil }
	req := TaskRequirementCommentWrite{Body: taskCommentFixtureBody}
	var pendingAttempt TaskRequirementCommentAttempt
	writeTaskCommentFixtureFile(t, root, "mode", "no-comment")
	_, err := service.SyncTaskRequirementComment(ctx, taskCommentFixtureLink, scope, req, persistTaskCommentFixture(root, &pendingAttempt), guard)
	var pending *TaskRequirementCommentUncertainError
	if !errors.As(err, &pending) || pendingAttempt.ReceiptID != "" || pendingAttempt.SendToken == "" {
		t.Fatalf("write with an ambiguous rejection = %v, intent %+v", err, pendingAttempt)
	}
	config, err := service.config.Load()
	if err != nil {
		t.Fatal(err)
	}
	paths := storage.NewPaths(filepath.Join(root, "other-home"))
	other := NewService(paths, storage.NewConfigStore(paths), nil)
	if err := other.config.Save(config); err != nil {
		t.Fatal(err)
	}
	writeTaskCommentFixtureFile(t, root, "mode", "")
	var otherAttempt TaskRequirementCommentAttempt
	id, err := other.SyncTaskRequirementComment(ctx, taskCommentFixtureLink, scope, req, persistTaskCommentFixture(root, &otherAttempt), guard)
	if err != nil || id != "1" || otherAttempt.SendToken == pendingAttempt.SendToken {
		t.Fatalf("other profile's write = %q, %v, intent %+v", id, err, otherAttempt)
	}
	id, err = service.CheckTaskRequirementComment(ctx, taskCommentFixtureLink, scope, pendingAttempt)
	if id != "" || !errors.As(err, &pending) {
		t.Fatalf("recheck attached another profile's comment: %q, %v", id, err)
	}
}
