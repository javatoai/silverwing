package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

const commentImageFixtureURL = "https://project.feishu.cn/goapi/v5/platform/file/stream/download/fixture-token"
const commentImageFixtureContent = "已完成验证\r\n\r\n![](" + commentImageFixtureURL + ")<!-- 938E4C1A-D9AE-40CF-870D-992EA4FD0E5C -->\r\n"

func TestRequirementCommentImagesSidecarAndCacheHit(t *testing.T) {
	service := &Service{paths: storage.NewPaths(t.TempDir())}
	config := domain.AppConfig{MeegleProjects: []domain.MeegleProjectConfig{{ProjectKey: "project-a", SimpleName: "space"}}}
	item := domain.ParticipatedWorkItem{ProjectKey: "project-a", ProjectName: "space", Type: "userstory", ID: "123", URL: "https://project.feishu.cn/space/userstory/detail/123"}
	session, _, err := service.requirementIdentityFor(context.Background(), config, "", false, func(context.Context, domain.AppConfig) (string, bool, error) {
		return "fixture-account", false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	key := requirementCacheKey("comments", session.raw, item)
	comments := domain.RequirementComments{Comments: []domain.RequirementComment{{ID: "comment-1", Content: commentImageFixtureContent}}}
	path, err := service.writeRequirementCommentsCache(key, comments, 123)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	generation := requirementCacheDigest(commentImageFixtureContent)
	image := []byte{137, 80, 78, 71, 13, 10, 26, 10, 1}
	resource, err := service.saveCommentImageResource(context.Background(), key, "comment-1", generation, commentImageFixtureURL, image, "")
	if err != nil || resource.Path == "" || resource.Generation != generation {
		t.Fatalf("save sidecar: resource=%#v err=%v", resource, err)
	}
	if data, err := os.ReadFile(resource.Path); err != nil || !bytes.Equal(data, image) {
		t.Fatalf("cached image does not match: %v", err)
	}
	value, err := service.readRequirementComments(context.Background(), config, map[string]any{"item": map[string]any{"projectKey": item.ProjectKey, "projectName": item.ProjectName, "type": item.Type, "id": item.ID, "url": item.URL}, "expectedIdentity": session.token})
	if err != nil {
		t.Fatal(err)
	}
	result := value.(RequirementCommentsReadResult)
	if result.Source != "CACHE" || len(result.CommentResources) != 1 || result.CommentResources[0].Path != resource.Path || result.Comments[0].Content != commentImageFixtureContent {
		t.Fatalf("cache hit lost runtime mapping: %#v", result)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("image cache rewrote original comment envelope")
	}
	otherKey := requirementCacheKey("comments", "other-account", item)
	if _, err := service.cachedCommentImageResource(otherKey, "comment-1", generation, commentImageFixtureURL); err == nil {
		t.Fatal("cross-account resource accepted")
	}
	if _, err := service.cachedCommentImageResource(key, "other-comment", generation, commentImageFixtureURL); err == nil {
		t.Fatal("cross-comment resource accepted")
	}
	if _, err := service.cachedCommentImageResource(key, "comment-1", generation, commentImageFixtureURL+"-other"); err == nil {
		t.Fatal("unlisted URL accepted")
	}
	if err := expectedRequirementDocumentIdentity(map[string]any{"expectedIdentity": opaqueRequirementIdentity("other-account")}, session.raw, false, nil); err == nil {
		t.Fatal("opaque account mismatch accepted")
	}
}

func TestRequirementCommentImagesRefreshCancellationAndUnsafeIndex(t *testing.T) {
	service := &Service{paths: storage.NewPaths(t.TempDir())}
	key := "isolated-comment-key"
	comments := domain.RequirementComments{Comments: []domain.RequirementComment{{ID: "comment-1", Content: commentImageFixtureContent}}}
	if _, err := service.writeRequirementCommentsCache(key, comments, 1); err != nil {
		t.Fatal(err)
	}
	generation := requirementCacheDigest(commentImageFixtureContent)
	resource, err := service.saveCommentImageResource(context.Background(), key, "comment-1", generation, commentImageFixtureURL, nil, "fixture failure")
	if err != nil || resource.Error != "fixture failure" || resource.Path != "" {
		t.Fatalf("failure sidecar: %#v %v", resource, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.saveCommentImageResource(ctx, key, "comment-1", generation, commentImageFixtureURL, nil, "cancelled"); err == nil {
		t.Fatal("cancelled write accepted")
	}
	resource, err = service.cachedCommentImageResource(key, "comment-1", generation, commentImageFixtureURL)
	if err != nil || resource.Error != "fixture failure" {
		t.Fatalf("cancel persisted failure: %#v %v", resource, err)
	}
	comments.Comments[0].Content = "changed\n\n" + commentImageFixtureContent
	if _, err := service.writeRequirementCommentsCache(key, comments, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := service.saveCommentImageResource(context.Background(), key, "comment-1", generation, commentImageFixtureURL, nil, "late failure"); err == nil {
		t.Fatal("late old-generation result accepted")
	}
	newGeneration := requirementCacheDigest(comments.Comments[0].Content)
	resource, err = service.cachedCommentImageResource(key, "comment-1", newGeneration, commentImageFixtureURL)
	if err != nil || resource.Path != "" || resource.Error != "" {
		t.Fatalf("new generation reused old state: %#v %v", resource, err)
	}
	index := requirementCommentImageIndex{Version: 1, Key: requirementCacheDigest(key), Images: map[string]map[string]requirementImageEntry{"comment-1": {commentImageFixtureURL: {Generation: newGeneration, RelativePath: "../outside.png"}}}}
	data, _ := json.Marshal(index)
	if err := atomicRequirementWrite(service.paths.Cache, filepath.Join(service.requirementCommentImageDirectory(key), "index.json"), data); err != nil {
		t.Fatal(err)
	}
	if _, err := service.cachedCommentImageResource(key, "comment-1", newGeneration, commentImageFixtureURL); err == nil {
		t.Fatal("sidecar path traversal accepted")
	}
}

func TestRequirementCommentImagesRejectCodeAndUnknownMetadata(t *testing.T) {
	for name, content := range map[string]string{
		"fenced":   "```md\n" + commentImageFixtureContent + "```",
		"indented": "    ![](" + commentImageFixtureURL + ")<!-- 938E4C1A-D9AE-40CF-870D-992EA4FD0E5C -->",
		"inline":   "`![](" + commentImageFixtureURL + ")<!-- 938E4C1A-D9AE-40CF-870D-992EA4FD0E5C -->`",
		"unknown":  "![](" + commentImageFixtureURL + ")<!-- keep -->",
	} {
		t.Run(name, func(t *testing.T) {
			service := &Service{paths: storage.NewPaths(t.TempDir())}
			if _, err := service.writeRequirementCommentsCache("key", domain.RequirementComments{Comments: []domain.RequirementComment{{ID: "c", Content: content}}}, 1); err != nil {
				t.Fatal(err)
			}
			if _, err := service.cachedCommentImageResource("key", "c", requirementCacheDigest(content), commentImageFixtureURL); err == nil {
				t.Fatal("non-image authorized authenticated download")
			}
		})
	}
}
