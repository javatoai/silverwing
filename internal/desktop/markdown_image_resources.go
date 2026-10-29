package desktop

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/editor"
)

type markdownImageScope struct {
	home, config, account             string
	taskID, taskPath, createdAt, root string
	path, linkRoot, source            string
	readerGeneration                  uint64
}

func (s *app) markdownImageScopeFor(state *markdownEditorState) markdownImageScope {
	scope := markdownImageScope{home: s.snapshot.Home, config: s.settingsUnit.state.configJSON,
		account: s.currentRequirementAccountIdentity(), readerGeneration: s.requirementUnit.state.requirementReaderGeneration,
		taskID: state.taskID, path: state.path, linkRoot: state.linkRoot}
	if !state.detached {
		task := s.taskUnit.currentTask()
		scope.taskPath, scope.createdAt, scope.root = task.path, task.createdAt, task.materialsPath
	}
	if state.doc != nil {
		scope.source = state.doc.Source()
	}
	return scope
}

func (s *app) markdownImagePoolFor(state *markdownEditorState) *nativeImagePool {
	if state.imagePool != nil && !state.imagePoolOwned {
		return state.imagePool
	}
	scope := s.markdownImageScopeFor(state)
	if state.imagePool == nil || state.imageScope != scope {
		state.imagePool.close()
		state.imagePool, state.imagePoolOwned, state.imageScope = s.newMarkdownImagePool(), true, scope
	}
	return state.imagePool
}

func markdownImageNodeKey(part editor.Part, node editor.Node) string {
	return fmt.Sprintf("%d:%d:%d", part.ID, node.Range.Start, node.Range.End)
}

func (s *app) markdownImageStateKey(state *markdownEditorState, pool *nativeImagePool, link string) string {
	key := strings.Join([]string{state.taskID, state.path, link}, "\x00")
	key += fmt.Sprintf("\x00pool:%d", pool.id)
	if isSkillMarkdownPreview(state) {
		key += "\x00" + state.linkRoot
	} else if state.detached {
		key += "\x00" + strings.TrimSpace(state.trustedImages[link])
	} else if state.taskID == s.taskUnit.currentTask().id {
		key += "\x00" + strings.TrimSpace(s.taskUnit.currentTask().materialsPath)
	}
	return key
}

// Backend comment images have a content-addressed basename. That verified
// content version lets separate comment-owned paths share immutable pixels;
// the enclosing pool still belongs to one reader/account/config snapshot.
// Ordinary local files retain their path and metadata version instead.
func markdownImageResourceKey(state *markdownEditorState, link, path string, remote bool) (string, error) {
	if remote {
		return "remote\x00" + link, nil
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("图片文件不可访问")
	}
	version := fmt.Sprintf("%s\x00%d\x00%d", path, info.Size(), info.ModTime().UnixNano())
	if state.commentReading && strings.Contains(filepath.ToSlash(path), "/requirement-comment-images/") {
		base := filepath.Base(path)
		if len(base) > 64 && (base[64] == '-' || base[64] == '.') {
			if digest, err := hex.DecodeString(base[:64]); err == nil && len(digest) == 32 {
				version = "content\x00" + base[:64]
			}
		}
	}
	return link + "\x00" + version, nil
}
