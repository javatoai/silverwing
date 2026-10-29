package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
)

func newReadonlyMarkdownState(key, source string, trustedImages map[string]string) *markdownEditorState {
	state := &markdownEditorState{
		taskID: key, doc: editor.NewDocument(source, key), readOnly: true, detached: true,
		trustedImages: cloneMarkdownTrustedImages(trustedImages), diagrams: make(map[uint64]*markdownDiagramState),
		inlineImages: make(map[string]*markdownInlineImageState), zoom: 1,
		readonlyReader: &readonlyMarkdownReaderState{zoom: 1},
	}
	return state
}

func updateReadonlyMarkdownState(state *markdownEditorState, source string, trustedImages map[string]string) {
	if state == nil {
		return
	}
	if !state.readOnly || !state.detached {
		return
	}
	if state.doc == nil {
		state.doc = editor.NewDocument(source, state.taskID)
	} else if state.doc.Source() != source {
		state.doc.SyncExternal(source)
	}
	closeMarkdownPreviewAssets(state)
	state.inlineImages = make(map[string]*markdownInlineImageState)
	state.diagrams = make(map[uint64]*markdownDiagramState)
	state.trustedImages = cloneMarkdownTrustedImages(trustedImages)
	state.closed = false
}

func closeMarkdownPreviewState(state *markdownEditorState) {
	if state == nil {
		return
	}
	closeMarkdownPreviewAssets(state)
	if state.readonlyReader != nil {
		state.readonlyReader.find.close()
		state.readonlyReader.findOpen = false
	}
	state.closed = true
}

func closeMarkdownPreviewAssets(state *markdownEditorState) {
	if state.imagePoolOwned {
		state.imagePool.close()
		state.imagePool = nil
	}
	if state.closeBitmapViewer != nil {
		closeViewer := state.closeBitmapViewer
		state.closeBitmapViewer = nil
		closeViewer()
	}
	for _, diagram := range state.diagrams {
		if diagram.cancel != nil {
			diagram.cancel()
			diagram.cancel = nil
		}
		diagram.loading = false
	}
	for _, image := range state.inlineImages {
		if image.cancel != nil {
			image.cancel()
			image.cancel = nil
		}
		image.loading = false
	}
}

func (s *app) pruneMarkdownImagePreviews(state *markdownEditorState, seen map[string]bool) {
	if state == nil {
		return
	}
	for key, image := range state.inlineImages {
		if seen[key] {
			continue
		}
		if image.cancel != nil {
			image.cancel()
		}
		delete(state.inlineImages, key)
	}
}

func (s *app) pruneMarkdownDiagramPreviews(state *markdownEditorState) {
	if state == nil || state.doc == nil {
		return
	}
	active := make(map[uint64]bool)
	for _, part := range state.doc.Parts() {
		if part.Kind == editor.PartMermaid {
			active[part.ID] = true
		}
	}
	for id, diagram := range state.diagrams {
		if active[id] {
			continue
		}
		if diagram.cancel != nil {
			diagram.cancel()
		}
		delete(state.diagrams, id)
	}
}

func cloneMarkdownTrustedImages(images map[string]string) map[string]string {
	if len(images) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(images))
	for link, path := range images {
		cloned[link] = path
	}
	return cloned
}

func sameMarkdownTrustedImages(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for link, path := range left {
		if right[link] != path {
			return false
		}
	}
	return true
}

func (s *app) renderMarkdownImage(c *ui.Context, p palette, state *markdownEditorState, part editor.Part, node editor.Node, size float32) {
	alt := strings.TrimSpace(node.Text)
	if alt == "" {
		alt = "图片"
	}
	image, err := s.ensureMarkdownImage(state, part, node)
	if err != nil {
		ui.Column(c).Shrink(0).Gap(3).Children(func() {
			label(c, "▧ "+alt, size, p.muted).Italic()
			label(c, err.Error(), 10, p.amber).Wrap()
		})
		return
	}
	if image.waiting {
		c.After(50 * time.Millisecond)
	}
	if image.bitmap != nil {
		width, height := image.bitmap.Size()
		if width < 1 || height < 1 {
			width, height = 320, 180
		}
		maxWidth := float32(520)
		if width > int(maxWidth) {
			height = int(float32(height) * maxWidth / float32(width))
			width = int(maxWidth)
		}
		ui.Column(c).Shrink(0).Gap(3).Children(func() {
			previewKey := markdownEditorControlKey(state, fmt.Sprintf("markdown-image-preview-%d-%d", part.ID, node.Range.Start))
			open := func() {
				if isCommentDraftMarkdownOwner(state) && s.requirementUnit.state.commentComposers != nil && s.requirementUnit.state.commentComposers.active != nil && s.requirementUnit.state.commentComposers.active.markdown == state {
					s.requirementUnit.state.commentComposers.active.imageFocusKey = previewKey
				}
				if state.commentReading && image.sourcePath != "" {
					s.openMarkdownLocalImageViewer(state, image.sourcePath)
					return
				}
				s.openMarkdownBitmapViewer(state, image.bitmap)
			}
			preview := ui.Image(c.Key(previewKey), image.bitmap).Width(float32(width)).MaxWidthPercent(100).AspectRatio(float32(width) / float32(height)).Fit(ui.Contain).Label("Markdown 图片：" + alt).OnClick(open)
			if isCommentDraftMarkdownOwner(state) {
				preview.Focusable().OnShortcut(0, ui.KeyEnter, open)
				if s.requirementUnit.state.commentComposers != nil && s.requirementUnit.state.commentComposers.active != nil {
					composer := s.requirementUnit.state.commentComposers.active
					if composer.markdown == state && composer.restoreImageFocus && composer.imageFocusKey == previewKey && s.materialsUnit.state.markdownBitmapViewer == nil && s.confirmationDialog == nil {
						preview.Focus()
						composer.restoreImageFocus = false
					}
				}
				if preview.Focused() {
					preview.Border(2, p.blue)
				}
			}
			if alt != "图片" {
				label(c, alt, 10, p.muted).Wrap()
			}
			if image.error != "" {
				label(c, "图片更新失败，仍显示上一次结果："+image.error, 10, p.amber).Wrap()
			}
		})
		return
	}
	caption := "正在加载图片…"
	if image.error != "" {
		caption = "图片加载失败：" + image.error
	}
	ui.Column(c).Shrink(0).Gap(3).Children(func() {
		label(c, "▧ "+alt, size, p.muted).Italic()
		label(c, caption, 10, map[bool]ui.Color{true: p.amber, false: p.muted}[image.error != ""]).Wrap()
	})
}

func (s *app) ensureMarkdownImage(state *markdownEditorState, part editor.Part, node editor.Node) (*markdownInlineImageState, error) {
	if state == nil || state.closed {
		return nil, fmt.Errorf("Markdown 图片预览已关闭")
	}
	if state.inlineImages == nil {
		state.inlineImages = make(map[string]*markdownInlineImageState)
	}
	link := strings.TrimSpace(node.Destination)
	if link == "" {
		return nil, fmt.Errorf("图片地址为空")
	}
	nodeKey := markdownImageNodeKey(part, node)
	if state.imageRenderSeen != nil {
		state.imageRenderSeen[nodeKey] = true
	}
	pool := s.markdownImagePoolFor(state)
	cacheKey := s.markdownImageStateKey(state, pool, link)
	image := state.inlineImages[nodeKey]
	if image != nil && image.key == cacheKey {
		return image, nil
	}
	if image != nil && image.cancel != nil {
		image.cancel()
	}
	image = &markdownInlineImageState{key: cacheKey, loading: true}
	state.inlineImages[nodeKey] = image
	var absolutePath string
	var remote bool
	if isSkillMarkdownPreview(state) {
		var err error
		absolutePath, remote, err = s.resolveSkillMarkdownImage(state, link)
		if err != nil {
			image.loading = false
			image.error = err.Error()
			return image, nil
		}
	} else if state.detached {
		absolutePath = strings.TrimSpace(state.trustedImages[link])
		if absolutePath == "" {
			image.loading = false
			image.error = "此只读内容没有可信的本地图片映射"
			return image, nil
		}
		validatedPath, err := validateTrustedMarkdownImage(absolutePath)
		if err != nil {
			image.loading = false
			image.error = err.Error()
			return image, nil
		}
		absolutePath = validatedPath
		if state.commentReading {
			image.sourcePath = validatedPath
		}
	} else {
		var err error
		absolutePath, remote, err = s.resolveTaskMarkdownImage(state, link)
		if err != nil {
			image.loading = false
			image.error = err.Error()
			return image, nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	image.cancel = cancel
	scope := s.markdownImageScopeFor(state)
	win := s.win
	if win == nil || win.IsDestroyed() {
		cancel()
		delete(state.inlineImages, nodeKey)
		image.loading, image.waiting = false, true
		return image, nil
	}
	complete := func(bitmap *ui.Bitmap, err error) {
		if win == nil || win.IsDestroyed() {
			return
		}
		win.Update(func() {
			if s.markdownPreviewJobsClosed() || s.win != win || ctx.Err() != nil || state.closed || state.inlineImages[nodeKey] != image || image.key == "" || s.markdownImageScopeFor(state) != scope {
				return
			}
			if isSkillMarkdownPreview(state) && !s.skillMarkdownPreviewCurrent(state) {
				return
			}
			if !state.detached {
				if state.binding != nil {
					if !s.markdownEditorIsCurrent(state) {
						return
					}
				} else if s.materialsUnit.state.markdown != state || state.taskID != s.taskUnit.currentTask().id || state.path != s.materialsUnit.state.documentPath {
					return
				}
			}
			image.loading = false
			image.cancel = nil
			if err != nil {
				image.error = err.Error()
				return
			}
			image.error = ""
			image.bitmap = bitmap
		})
	}
	resourceKey, resourceErr := markdownImageResourceKey(state, link, absolutePath, remote)
	if resourceErr != nil {
		image.loading, image.error = false, resourceErr.Error()
		cancel()
		return image, nil
	}
	load := func(shared context.Context) (*ui.Bitmap, error) {
		return decodeNativeImagePreview(shared, absolutePath, 1200)
	}
	if remote {
		remoteTarget := link
		if parsed, err := url.Parse(strings.ReplaceAll(link, " ", "%20")); err == nil {
			parsed.Scheme = strings.ToLower(parsed.Scheme)
			remoteTarget = parsed.String()
		}
		load = func(shared context.Context) (*ui.Bitmap, error) {
			requestCtx, stop := context.WithTimeout(shared, nativeRemoteImageTimeout)
			defer stop()
			return fetchNativeRemoteImagePreview(requestCtx, remoteTarget, 1200)
		}
	}
	unsubscribe, err := pool.subscribe(ctx, resourceKey, load, complete)
	if err != nil {
		cancel()
		image.loading = false
		if errors.Is(err, errNativeImageQueueFull) {
			delete(state.inlineImages, nodeKey)
			image.waiting = true
		} else {
			image.error = err.Error()
		}
		return image, nil
	}
	image.cancel = func() { cancel(); unsubscribe() }
	return image, nil
}

func (s *app) resolveTaskMarkdownImage(state *markdownEditorState, link string) (string, bool, error) {
	task := s.taskUnit.currentTask()
	rootPath, rootLabel := task.materialsPath, "资料"
	if state.binding != nil {
		if !s.markdownEditorIsCurrent(state) || state.path == "" {
			return "", false, fmt.Errorf("当前说明已切换，无法读取此图片")
		}
		if state.taskID != "" {
			if state.taskID != task.id || task.path == "" {
				return "", false, fmt.Errorf("当前说明已切换，无法读取此图片")
			}
			rootPath = task.path
		} else {
			rootPath = filepath.Dir(state.path)
		}
		rootLabel = "说明"
	} else if state.taskID == "" || state.taskID != task.id || state.path == "" || state.path != s.materialsUnit.state.documentPath {
		return "", false, fmt.Errorf("当前资料已切换，无法读取此图片")
	}
	parsed, err := url.Parse(strings.ReplaceAll(link, " ", "%20"))
	if err != nil {
		return "", false, fmt.Errorf("图片地址无效")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		if parsed.Hostname() == "" || parsed.User != nil {
			return "", false, fmt.Errorf("远程图片地址无效")
		}
		if !state.remoteImages {
			return "", false, fmt.Errorf("此只读内容不允许自动加载远程图片")
		}
		return "", true, nil
	case "":
	default:
		return "", false, fmt.Errorf("仅支持%s目录内的相对图片路径", rootLabel)
	}
	if parsed.Host != "" || strings.HasPrefix(link, "/") || strings.HasPrefix(link, "\\") || filepath.IsAbs(parsed.Path) {
		return "", false, fmt.Errorf("图片路径必须位于当前%s目录内", rootLabel)
	}
	if parsed.Path == "" {
		return "", false, fmt.Errorf("图片路径为空")
	}
	root, err := filepath.Abs(rootPath)
	if err != nil {
		return "", false, fmt.Errorf("无法解析%s根目录", rootLabel)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", false, fmt.Errorf("%s根目录不可访问", rootLabel)
	}
	source, err := filepath.Abs(state.path)
	if err != nil {
		return "", false, fmt.Errorf("Markdown 文档路径无效")
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return "", false, fmt.Errorf("Markdown 文档不可访问")
	}
	if !markdownPathWithin(root, source) {
		return "", false, fmt.Errorf("Markdown 文档不在当前%s目录内", rootLabel)
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(source), filepath.FromSlash(parsed.Path)))
	if !markdownPathWithin(root, path) {
		return "", false, fmt.Errorf("图片路径不能越出当前%s目录", rootLabel)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." {
		return "", false, fmt.Errorf("图片路径无效")
	}
	cursor := root
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		if segment == "" || segment == "." {
			continue
		}
		cursor = filepath.Join(cursor, segment)
		info, statErr := os.Lstat(cursor)
		if statErr != nil {
			return "", false, fmt.Errorf("图片文件不可访问")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", false, fmt.Errorf("不支持通过符号链接读取资料图片")
		}
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("图片必须是%s目录内的普通文件", rootLabel)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !markdownPathWithin(root, resolved) {
		return "", false, fmt.Errorf("图片路径不在当前%s目录内", rootLabel)
	}
	return resolved, false, nil
}

func validateTrustedMarkdownImage(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("可信图片路径无效")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("可信图片不可访问")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("可信图片必须是普通文件")
	}
	return resolved, nil
}

func markdownPathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
