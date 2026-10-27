//go:build windows

package integrations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

func TestRequirementCommentImagesAuthenticatedAttachmentRoute(t *testing.T) {
	for name, content := range map[string]string{
		"original UUID annotation": commentImageFixtureContent,
		"SW unannotated readback":  "# 你好\n\n![](" + commentImageFixtureURL + ")",
	} {
		t.Run(name, func(t *testing.T) { testCommentImageAttachmentRoute(t, content) })
	}
}

func testCommentImageAttachmentRoute(t *testing.T, content string) {
	root := t.TempDir()
	paths := storage.NewPaths(filepath.Join(root, "isolated-home"))
	control, calls := filepath.Join(root, "fixture.json"), filepath.Join(root, "calls.log")
	writeRequirementCacheV2Fixture(t, control, map[string]string{"calls": calls})
	t.Setenv("SILVERWING_REQUIREMENT_CACHE_FIXTURE", control)
	const attachment = `if ($arguments.Length -ge 2 -and $arguments[0] -eq 'attachment' -and $arguments[1] -eq '+download') {
  $outputPath = ''
  for ($i = 0; $i -lt $arguments.Length - 1; $i++) { if ($arguments[$i] -eq '--output') { $outputPath = $arguments[$i + 1] } }
  [System.IO.File]::WriteAllBytes($outputPath, [byte[]]@(137,80,78,71,13,10,26,10,1))
  exit 0
}
`
	script := strings.Replace(requirementCacheV2FixtureScript, "  [Console]::Error.WriteLine('fixture rejects unknown command')", attachment+"  [Console]::Error.WriteLine('fixture rejects unknown command')", 1)
	cliPath := filepath.Join(root, "fixture.ps1")
	if err := os.WriteFile(cliPath, append([]byte{0xef, 0xbb, 0xbf}, []byte(script)...), 0o600); err != nil {
		t.Fatal(err)
	}
	config := domain.DefaultAppConfig()
	config.MeegleExecutablePath = &cliPath
	config.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "project-a", SimpleName: "space"}}
	service := NewService(paths, nil, process.NewRunner())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	state, token, err := service.RequirementIdentity(ctx, config)
	if err != nil || state != requirementIdentityAuthenticated {
		t.Fatalf("fixture identity: %s %v", state, err)
	}
	session, _, err := service.requirementIdentityFor(ctx, config, token, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	item := domain.ParticipatedWorkItem{ProjectKey: "project-a", ProjectName: "space", Type: "userstory", ID: "123", URL: "https://project.feishu.cn/space/userstory/detail/123"}
	key := requirementCacheKey("comments", session.raw, item)
	if _, err := service.writeRequirementCommentsCache(key, domain.RequirementComments{Comments: []domain.RequirementComment{{ID: "c", Content: content}}}, 1); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"url": item.URL, "projectKey": item.ProjectKey, "projectName": item.ProjectName, "id": item.ID, "type": item.Type,
		"scope": "comment", "commentID": "c", "commentGeneration": requirementCacheDigest(content), "fileUrl": commentImageFixtureURL, "expectedIdentity": token}
	for attempt := 0; attempt < 2; attempt++ {
		value, err := service.downloadRequirementAttachment(ctx, config, payload)
		if err != nil {
			t.Fatalf("fixture image read %d: %v", attempt, err)
		}
		resource, ok := value.(RequirementCommentResource)
		if !ok || resource.CommentID != "c" || resource.Path == "" || resource.Error != "" {
			t.Fatalf("unexpected attachment result type=%T", value)
		}
	}
	if count := requirementCacheV2CountCalls(requirementCacheV2FixtureCalls(t, calls), "attachment +download "); count != 1 {
		t.Fatalf("cached second request downloaded again: %d", count)
	}
	payload["expectedIdentity"] = opaqueRequirementIdentity("other-account")
	if _, err := service.downloadRequirementAttachment(ctx, config, payload); err == nil {
		t.Fatal("attachment route accepted another account token")
	}
	payload["expectedIdentity"] = token
	payload["fileUrl"] = "https://example.invalid/unlisted.png"
	if _, err := service.downloadRequirementAttachment(ctx, config, payload); err == nil {
		t.Fatal("attachment route accepted an unlisted URL")
	}
	if count := requirementCacheV2CountCalls(requirementCacheV2FixtureCalls(t, calls), "attachment +download "); count != 1 {
		t.Fatalf("rejected resource invoked downloader: %d", count)
	}
}
