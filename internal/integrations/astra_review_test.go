package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

func TestAstraIntegrationsImageDestinationRoundTrip(t *testing.T) {
	const first = "https://example.invalid/first.png"
	const second = "https://example.invalid/second-long-name.png"
	cases := []string{
		"![first](<" + first + "> \"caption\")\n",
		"![second][b]\n![first][a]\n\n[a]: <" + first + ">\n[b]: " + second + "\n",
		"![first][a]\n![second](" + second + ")\n\n[a]: " + first + "\n",
	}
	for _, body := range cases {
		rewritten := replaceRequirementImageDestinations(body, map[string]string{first: "assets/a.png", second: "assets/b.png"})
		if strings.Contains(rewritten, "<<") || strings.Contains(rewritten, "https://example.invalid") {
			t.Fatalf("invalid rewritten destinations: %q", rewritten)
		}
		restored := replaceRequirementImageDestinations(rewritten, map[string]string{"assets/a.png": first, "assets/b.png": second})
		if restored != body {
			t.Fatalf("Markdown did not round-trip:\nwant %q\ngot  %q", body, restored)
		}
	}
	service := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	item := domain.ParticipatedWorkItem{ProjectKey: "project", Type: "userstory", ID: "123", URL: "https://project.feishu.cn/space/userstory/detail/123"}
	body := cases[1]
	key := requirementCacheKey("body", "account", item)
	document, err := service.writeRequirementDocument(key, item, body, 1)
	if err != nil {
		t.Fatal(err)
	}
	image := []byte{137, 80, 78, 71, 13, 10, 26, 10, 1}
	for _, imageURL := range []string{first, second} {
		document, err = service.saveRequirementImage(key, document.Index.Generation, imageURL, image)
		if err != nil {
			t.Fatal(err)
		}
		if document.Body != body {
			t.Fatalf("cached image changed source Markdown: %q", document.Body)
		}
	}
}

func TestAstraIntegrationsCandidateTitlesKeepAccountContext(t *testing.T) {
	service := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	fixtureCLI := "controlled-nonexistent-fixture-cli"
	config := domain.AppConfig{MeegleExecutablePath: &fixtureCLI, MeegleProjects: []domain.MeegleProjectConfig{
		{ProjectKey: "project-a", SimpleName: "space-a"}, {ProjectKey: "project-b", SimpleName: "space-b"},
	}}
	session, _, err := service.requirementIdentityFor(context.Background(), config, "", false,
		func(context.Context, domain.AppConfig) (string, bool, error) { return "fixture-account", false, nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range config.MeegleProjects {
		item := domain.ParticipatedWorkItem{ProjectKey: project.ProjectKey, Type: "userstory", ID: "123",
			URL: "https://project.feishu.cn/" + project.SimpleName + "/userstory/detail/123"}
		title := "Cached " + project.ProjectKey
		_, err := service.writeRequirementMetadataCache(requirementCacheKey("metadata", session.raw, item),
			RequirementMetadataReadResult{ID: item.ID, ProjectKey: item.ProjectKey, Type: item.Type, Title: &title}, 1234)
		if err != nil {
			t.Fatal(err)
		}
	}
	var workers sync.WaitGroup
	for _, project := range config.MeegleProjects {
		workers.Go(func() {
			link := requirementCandidateLink{projectKey: project.ProjectKey, url: "https://project.feishu.cn/" + project.SimpleName + "/userstory/detail/123"}
			title, err := service.readRequirementCandidateTitle(context.Background(), config, link)
			if err != nil || title != "Cached "+project.ProjectKey {
				t.Errorf("cached multi-project title was lost: title=%q err=%v", title, err)
			}
		})
	}
	workers.Wait()
	if err := service.verifyRequirementIdentitySession(context.Background(), config, session.raw, session.generation); err != nil {
		t.Fatalf("candidate title lookup invalidated the shared account session: %v", err)
	}
}

func TestAstraIntegrationsCleanupFailureSurvivesCancellation(t *testing.T) {
	service := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	failure := &process.CleanupError{Phase: "fixture", Cause: context.Canceled}
	ctx, cancel := context.WithCancel(context.Background())
	request := &requirementIdentityRequest{ctx: ctx, cancel: cancel, done: make(chan struct{}), contextKey: "fixture"}
	service.runRequirementIdentityProbe(request, domain.AppConfig{}, func(context.Context, domain.AppConfig) (string, bool, error) {
		cancel()
		return "", false, failure
	})
	if !process.IsCleanupFailure(request.err) {
		t.Fatalf("identity probe hid cleanup uncertainty: %v", request.err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	op := &requirementDocumentOperation{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	body := &requirementBodyRequest{op: op}
	coordinator := service.requirementDocumentCoordinator()
	coordinator.runBody("fixture", body, func(context.Context, *requirementBodyRequest) (RequirementReadResult, error) {
		cancel()
		return RequirementReadResult{}, failure
	})
	if !process.IsCleanupFailure(body.err) {
		t.Fatalf("body coordinator hid cleanup uncertainty: %v", body.err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	_, err := coordinator.image(ctx, "fixture-image", func(context.Context) (any, error) {
		cancel()
		return nil, failure
	})
	if !process.IsCleanupFailure(err) {
		t.Fatalf("image coordinator hid cleanup uncertainty: %v", err)
	}
	_, err = runInputFile(ctx, "fixture", func([]string) (process.Result, error) { return process.Result{}, failure }, nil, "fixture", func() {})
	if !process.IsCleanupFailure(err) {
		t.Fatalf("CLI input wrapper hid cleanup uncertainty: %v", err)
	}
}

func TestAstraIntegrationsMaterialsFencePreservesCode(t *testing.T) {
	for _, content := range []string{
		"````markdown\r\n```\r\n![inside](missing.png)\r\n````\r\n![outside](real.png)\r\n",
		"~~~~\n~~~\n![inside](missing.png)\n~~~~ with suffix\n![inside](missing.png)\n~~~~\n![outside](real.png)\n",
		"    ```\n![outside](real.png)\n",
	} {
		got, err := materialsMarkdownText(content, func(text string) (string, error) {
			if strings.Contains(text, "missing.png") {
				return "", errors.New("code image reached resource resolver")
			}
			return strings.ReplaceAll(text, "real.png", "remote.png"), nil
		})
		want := strings.ReplaceAll(content, "real.png", "remote.png")
		if err != nil || got != want {
			t.Fatalf("fenced or indented code not preserved: got=%q err=%v", got, err)
		}
	}
}

func TestAstraIntegrationsLastSubscriberReportsCleanupFailure(t *testing.T) {
	for _, kind := range []string{"identity", "body"} {
		t.Run(kind, func(t *testing.T) {
			service := NewService(storage.NewPaths(t.TempDir()), nil, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{})
			result := make(chan error, 1)
			failure := &process.CleanupError{Phase: "fixture retired worker", Cause: context.Canceled}
			go func() {
				if kind == "identity" {
					_, _, err := service.requirementIdentityFor(ctx, domain.AppConfig{}, "", true,
						func(shared context.Context, _ domain.AppConfig) (string, bool, error) {
							close(started)
							<-shared.Done()
							return "", false, failure
						})
					result <- err
				} else {
					_, err := service.requirementDocumentCoordinator().read(ctx, "fixture", false,
						func(shared context.Context, _ *requirementBodyRequest) (RequirementReadResult, error) {
							close(started)
							<-shared.Done()
							return RequirementReadResult{}, failure
						})
					result <- err
				}
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("shared worker did not start")
			}
			cancel()
			select {
			case err := <-result:
				if !process.IsCleanupFailure(err) {
					t.Fatalf("last subscriber lost cleanup failure: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("last subscriber did not join canceled worker")
			}
		})
	}
}

type astraUnconfirmedGateway struct {
	domain.FeishuMaterialsGateway
	parent   domain.FeishuWikiNode
	attempts int
}

func (g *astraUnconfirmedGateway) GetNode(string) (*domain.FeishuWikiNode, error) {
	return &g.parent, nil
}

func (g *astraUnconfirmedGateway) Version(domain.FeishuWikiNode, bool) (domain.FeishuRemoteVersion, error) {
	return domain.FeishuRemoteVersion{Version: "1"}, nil
}

func (g *astraUnconfirmedGateway) ListChildren(domain.FeishuWikiNode) ([]domain.FeishuWikiNode, error) {
	return nil, nil
}

func (g *astraUnconfirmedGateway) CreateDirectory(domain.FeishuWikiNode, string) (domain.FeishuWriteResult, error) {
	g.attempts++
	return domain.FeishuWriteResult{}, &materialsUnconfirmedCreationError{message: "fixture ambiguous create"}
}

func TestAstraIntegrationsGatewayAmbiguousCreateCannotRetry(t *testing.T) {
	service := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	parent := domain.FeishuWikiNode{SpaceID: "space", NodeToken: "root", ObjectToken: "root-object", ObjectType: "docx", ParentNodeToken: "target", Title: "Root"}
	entry := domain.MaterialsSyncEntry{Path: materialsRequirementRoot, Kind: domain.MaterialsSyncDirectory, NodeToken: parent.NodeToken,
		ObjectToken: parent.ObjectToken, ObjectType: parent.ObjectType, ParentNodeToken: parent.ParentNodeToken, Title: parent.Title,
		RemoteVersion: domain.FeishuRemoteVersion{Version: "1"}}
	plan := domain.MaterialsSyncPlan{ScopeID: "fixture-scope", SourceID: "fixture-source", Target: domain.FeishuWikiNode{SpaceID: "space", NodeToken: "target"}}
	state := remoteMaterialsState{root: parent, value: domain.MaterialsSyncManifest{Namespace: "fixture-namespace"}}
	journal, err := service.openMaterialsReceiptJournal(plan, state)
	if err != nil {
		t.Fatal(err)
	}
	coordinator := &materialsWriteCoordinator{plan: plan, ops: materialsResourceOperations{journal: journal, verify: func(localMaterial) error { return nil }}}
	work := materialsCreateWork{file: localMaterial{path: materialsFilesRoot, kind: domain.MaterialsSyncDirectory}, parent: parent,
		intent: domain.MaterialsSyncIntent{OperationID: "fixture-operation", Path: materialsFilesRoot, Kind: domain.MaterialsSyncDirectory,
			Phase: "create", ParentNodeToken: parent.NodeToken, Title: "Materials", TemporaryTitle: "Temporary"}}
	binding := domain.MaterialsSyncBinding{Entries: map[string]domain.MaterialsSyncEntry{materialsRequirementRoot: entry}}
	gateway := &astraUnconfirmedGateway{parent: parent}
	_, err = coordinator.createOne(context.Background(), gateway, newMaterialsSnapshots(gateway), binding, work)
	if err == nil || gateway.attempts != 1 {
		t.Fatalf("fixture did not reach an ambiguous create: attempts=%d err=%v", gateway.attempts, err)
	}
	journal, err = service.openMaterialsReceiptJournal(plan, state)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.ops.journal = journal
	work.resuming = true
	_, err = coordinator.createOne(context.Background(), gateway, newMaterialsSnapshots(gateway), binding, work)
	if err == nil || gateway.attempts != 1 {
		t.Fatalf("invisible ambiguous create was retried after journal reload: attempts=%d err=%v", gateway.attempts, err)
	}
}

func TestAstraIntegrationsRequirementTargetConfiguredProject(t *testing.T) {
	service := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	config := domain.DefaultAppConfig()
	config.MeegleProjects = []domain.MeegleProjectConfig{{SimpleName: "space", ProjectKey: "current"}}
	for _, nested := range []bool{false, true} {
		item := map[string]any{"url": "https://project.feishu.cn/space/userstory/detail/123", "projectKey": "old"}
		payload := item
		if nested {
			payload = map[string]any{"item": item}
		}
		if _, err := service.requirementTarget(context.Background(), config, payload); err == nil {
			t.Fatal("accepted project key inconsistent with current configuration")
		}
		delete(item, "projectKey")
		resolved, err := service.requirementTarget(context.Background(), config, payload)
		if err != nil || resolved.ProjectKey != "current" {
			t.Fatalf("valid configured target rejected: %+v %v", resolved, err)
		}
	}
}

func TestAstraIntegrationsRejectPartialCLIJSONAndIntegers(t *testing.T) {
	for _, raw := range []string{`{"comments":[]} {"error":"late failure"}`, `{"authenticated":true} garbage`} {
		if _, err := decodeJSON(raw); err == nil {
			t.Fatalf("accepted trailing CLI output: %q", raw)
		}
	}
	value, err := decodeJSON("{\"count\":9007199254740993}\n")
	if err != nil || object(value)["count"] != json.Number("9007199254740993") {
		t.Fatalf("valid number lost precision: %v %v", value, err)
	}
	for _, raw := range []string{"1garbage", "1.5", "1 2", "1e3", "9223372036854775808"} {
		if _, ok := integer(raw); ok {
			t.Fatalf("accepted malformed integer %q", raw)
		}
	}
	if value, ok := integer(" 123 "); !ok || value != 123 {
		t.Fatal("valid integer rejected")
	}
}
