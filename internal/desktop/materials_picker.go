package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"

	"github.com/egoist/mygo/ui"
)

type materialsPickerState struct {
	quickOpen     bool
	quickQuery    string
	quickSelected int

	searchOpen       bool
	searchQuery      string
	searchResult     *documents.SearchResult
	searchProgress   documents.MaterialsSearchProgress
	searchLoading    bool
	searchError      string
	searchStopped    bool
	searchFailures   bool
	searchSelected   int
	searchCancel     context.CancelFunc
	searchDebounce   *time.Timer
	searchGeneration uint64
	searchTaskID     string

	pendingHit     *documents.SearchHit
	pendingHitTask string
	pendingHitPath string

	sourcePreviewPath   string
	sourcePreviewHit    *documents.SearchHit
	sourcePreviewReveal bool
}

func (s *materialsController) ensureMaterialsPickerState() *materialsPickerState {
	if s.state.materialsPicker == nil {
		s.state.materialsPicker = &materialsPickerState{}
	}
	return s.state.materialsPicker
}

func (s *materialsController) openMaterialsQuickPicker() {
	state := s.ensureMaterialsPickerState()
	state.quickQuery = ""
	state.quickSelected = 0
	state.quickOpen = true
}

func (s *app) openMaterialsFullTextSearch() {
	state := s.materialsUnit.ensureMaterialsPickerState()
	s.materialsUnit.cancelMaterialsSearchRun(state)
	state.searchGeneration++
	state.searchOpen = true
	state.searchQuery = ""
	state.searchResult = nil
	state.searchProgress = documents.MaterialsSearchProgress{}
	state.searchLoading = false
	state.searchError = ""
	state.searchStopped = false
	state.searchFailures = false
	state.searchSelected = 0
	state.searchTaskID = s.taskUnit.currentTask().id
}

func (s *materialsController) cancelMaterialsSearch() {
	state := s.state.materialsPicker
	if state == nil {
		return
	}
	s.cancelMaterialsSearchRun(state)
	state.searchGeneration++
	state.quickOpen = false
	state.searchOpen = false
	state.searchResult = nil
	state.pendingHit = nil
	state.pendingHitTask = ""
	state.pendingHitPath = ""
	state.sourcePreviewPath = ""
	state.sourcePreviewHit = nil
	state.sourcePreviewReveal = false
}

func (s *app) materialsSearchSourcePreviewActive() bool {
	state := s.materialsUnit.state.materialsPicker
	return state != nil && state.sourcePreviewHit != nil && state.sourcePreviewPath != "" &&
		filepath.Clean(state.sourcePreviewPath) == filepath.Clean(s.materialsUnit.state.documentPath) && s.materialsUnit.state.documentKind != "pdf"
}

func (s *app) materialsSearchSourcePreviewHit() (documents.SearchHit, bool) {
	if !s.materialsSearchSourcePreviewActive() {
		return documents.SearchHit{}, false
	}
	return *s.materialsUnit.state.materialsPicker.sourcePreviewHit, true
}

func (s *materialsController) closeMaterialsSearchSourcePreview() {
	state := s.state.materialsPicker
	if state == nil {
		return
	}
	state.sourcePreviewPath = ""
	state.sourcePreviewHit = nil
	state.sourcePreviewReveal = false
}

func (s *materialsController) cancelMaterialsSearchRun(state *materialsPickerState) {
	if state.searchDebounce != nil {
		state.searchDebounce.Stop()
		state.searchDebounce = nil
	}
	if state.searchCancel != nil {
		state.searchCancel()
		state.searchCancel = nil
	}
	state.searchLoading = false
}

func (s *app) closeMaterialsFullTextSearch(stopped bool) {
	state := s.materialsUnit.ensureMaterialsPickerState()
	s.materialsUnit.cancelMaterialsSearchRun(state)
	state.searchGeneration++
	state.searchOpen = false
	state.searchStopped = stopped
}

func (s *app) queueMaterialsSearch() {
	state := s.materialsUnit.ensureMaterialsPickerState()
	s.materialsUnit.cancelMaterialsSearchRun(state)
	state.searchGeneration++
	generation := state.searchGeneration
	state.searchTaskID = s.taskUnit.currentTask().id
	state.searchResult = nil
	state.searchProgress = documents.MaterialsSearchProgress{}
	state.searchError = ""
	state.searchStopped = false
	state.searchSelected = 0
	query, taskID := state.searchQuery, state.searchTaskID
	if query == "" {
		return
	}
	state.searchLoading = true
	state.searchDebounce = time.AfterFunc(180*time.Millisecond, func() {
		win := s.win
		if win == nil || win.IsDestroyed() {
			return
		}
		win.Update(func() {
			current := s.materialsUnit.state.materialsPicker
			if current != state || state.searchGeneration != generation || !state.searchOpen || s.taskUnit.currentTask().id != taskID {
				return
			}
			state.searchDebounce = nil
			ctx, cancel := context.WithCancel(context.Background())
			state.searchCancel = cancel
			go s.runMaterialsSearch(ctx, state, generation, taskID, query)
		})
	})
}

func (s *app) runMaterialsSearch(ctx context.Context, state *materialsPickerState, generation uint64, taskID, query string) {
	ctx = documents.WithMaterialsSearchProgress(ctx, func(progress documents.MaterialsSearchProgress) {
		win := s.win
		if win == nil || win.IsDestroyed() {
			return
		}
		win.Update(func() {
			if s.materialsUnit.state.materialsPicker != state || state.searchGeneration != generation || !state.searchOpen || s.taskUnit.currentTask().id != taskID {
				return
			}
			state.searchProgress = progress
		})
	})
	var result application.ActionResult
	err := s.acquireBackend(ctx)
	if err == nil {
		if s.backend == nil {
			err = errors.New("应用后端不可用")
		} else {
			result, err = s.backend.Dispatch(ctx, application.Action{
				Kind: "document.search", TaskID: taskID,
				Payload: map[string]any{"query": query},
			})
		}
		s.releaseBackend()
	}
	win := s.win
	if win == nil || win.IsDestroyed() {
		return
	}
	win.Update(func() {
		if s.materialsUnit.state.materialsPicker != state || state.searchGeneration != generation || !state.searchOpen || s.taskUnit.currentTask().id != taskID {
			return
		}
		state.searchCancel = nil
		state.searchLoading = false
		if ctx.Err() != nil {
			state.searchStopped = true
			return
		}
		if err != nil {
			state.searchError = err.Error()
			return
		}
		raw, marshalErr := json.Marshal(result.Data)
		var searchResult documents.SearchResult
		decodeErr := marshalErr
		if decodeErr == nil {
			decodeErr = json.Unmarshal(raw, &searchResult)
		}
		if decodeErr != nil {
			state.searchError = "全文搜索结果格式错误：" + decodeErr.Error()
			return
		}
		state.searchResult = &searchResult
		state.searchSelected = 0
	})
}

func (s *app) stopMaterialsSearch() {
	state := s.materialsUnit.ensureMaterialsPickerState()
	if state.searchCancel != nil {
		state.searchCancel()
		state.searchCancel = nil
	}
	if state.searchDebounce != nil {
		state.searchDebounce.Stop()
		state.searchDebounce = nil
	}
	state.searchGeneration++
	state.searchLoading = false
	state.searchStopped = true
}

func (s *app) materialQuickPickerDialog(c *ui.Context, p palette) {
	state := s.materialsUnit.ensureMaterialsPickerState()
	dialog := ui.DialogBase(c.Key("materials-quick-picker"), &state.quickOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .34))
		width, _ := c.Size()
		panel.Width(maxFloat32(360, minFloat32(560, width-64))).MaxHeight(500).Padding(18).Gap(10).Background(p.surface).Border(1, p.line).Radius(12)
		panel.OnShortcut(0, ui.KeyEscape, func() { state.quickOpen = false })
		panel.OnShortcut(0, ui.KeyDown, func() { s.moveQuickPickerSelection(state, 1) })
		panel.OnShortcut(0, ui.KeyUp, func() { s.moveQuickPickerSelection(state, -1) })
		panel.OnShortcut(0, ui.KeyEnter, func() { s.openSelectedQuickPickerEntry(state) })
		panel.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				label(c, "快速打开文件", 16, p.text).FontWeight(600).Grow(1)
				if action(c, "materials-quick-picker-close", "close", "关闭快速打开", p, false) {
					state.quickOpen = false
				}
			})
			ui.TextInput(c.Key("materials-quick-picker-query"), &state.quickQuery).FillWidth().Height(38).Placeholder("输入文件名或路径").AutoFocus().OnChange(func() {
				state.quickSelected = 0
			})
			matches := s.materialQuickPickerMatches(state.quickQuery)
			ui.Scroll(c).Grow(1).MinHeight(160).MaxHeight(340).Gap(3).Children(func() {
				if len(matches) == 0 {
					message := "没有匹配的文件"
					if !s.materialsUnit.state.documentsLoaded {
						message = "正在读取任务资料目录…"
					}
					label(c, message, 12, p.muted).Padding(8)
					return
				}
				for index, item := range matches {
					selected := index == state.quickSelected
					row := ui.ButtonBase(c.Key(fmt.Sprintf("materials-quick-entry-%d", index))).Column().FillWidth().Padding(8, 10).Gap(3).Radius(7).Label(item.relative)
					if selected {
						row.Background(p.selected)
					} else if row.Hovered() {
						row.Background(p.soft)
					}
					if selected {
						row.ScrollIntoView()
					}
					row.Children(func() {
						label(c, item.name, 12, p.text).FontWeight(500).SingleLine()
						if item.relative != item.name {
							label(c, item.relative, 10, p.muted).SingleLine()
						}
					})
					if row.Clicked() {
						state.quickSelected = index
						s.selectQuickPickerEntry(item)
					}
				}
			})
			label(c, "↑ ↓ 选择 · Enter 打开 · Esc 关闭", 10, p.muted)
		})
	})
	if dialog.Dismissed() {
		state.quickOpen = false
	}
}

func (s *app) materialQuickPickerMatches(query string) []quickPickerEntry {
	return s.materialsDocumentProjection().quickMatches(query)
}

func (s *app) moveQuickPickerSelection(state *materialsPickerState, delta int) {
	if state == nil {
		return
	}
	matches := s.materialQuickPickerMatches(state.quickQuery)
	if len(matches) == 0 {
		state.quickSelected = 0
		return
	}
	state.quickSelected = max(0, min(len(matches)-1, state.quickSelected+delta))
}

func (s *app) openSelectedQuickPickerEntry(state *materialsPickerState) {
	if state == nil {
		return
	}
	matches := s.materialQuickPickerMatches(state.quickQuery)
	if state.quickSelected >= 0 && state.quickSelected < len(matches) {
		s.selectQuickPickerEntry(matches[state.quickSelected])
	}
}

func (s *app) selectQuickPickerEntry(item quickPickerEntry) {
	if s.materialsFileReadBlocked() {
		return
	}
	state := s.materialsUnit.ensureMaterialsPickerState()
	state.quickOpen = false
	s.selectMaterialsPath(item.path)
}

func (s *app) materialFullTextSearchDialog(c *ui.Context, p palette) {
	state := s.materialsUnit.ensureMaterialsPickerState()
	dialog := ui.DialogBase(c.Key("materials-full-text-search"), &state.searchOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .34))
		width, _ := c.Size()
		panel.Width(maxFloat32(400, minFloat32(720, width-64))).MaxHeight(640).Padding(18).Gap(10).Background(p.surface).Border(1, p.line).Radius(12)
		panel.OnShortcut(0, ui.KeyEscape, func() { s.closeMaterialsFullTextSearch(false) })
		panel.OnShortcut(0, ui.KeyDown, func() { s.moveMaterialsSearchSelection(state, 1) })
		panel.OnShortcut(0, ui.KeyUp, func() { s.moveMaterialsSearchSelection(state, -1) })
		panel.OnShortcut(0, ui.KeyEnter, func() { s.openSelectedMaterialsSearchHit(state) })
		panel.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				label(c, "搜索任务资料", 16, p.text).FontWeight(600).Grow(1)
				if action(c, "materials-full-text-search-close", "close", "关闭全文搜索", p, false) {
					s.closeMaterialsFullTextSearch(false)
				}
			})
			ui.TextInput(c.Key("materials-full-text-search-query"), &state.searchQuery).FillWidth().Height(38).Placeholder("搜索当前任务的全部资料").AutoFocus().OnChange(func() {
				s.queueMaterialsSearch()
			})
			ui.Column(c).FillWidth().Grow(1).MinHeight(180).MaxHeight(430).Gap(8).Children(func() {
				s.materialsSearchContent(c, p, state)
			})
			ui.Divider(c)
			label(c, "↑ ↓ 选择 · Enter 打开位置 · Esc 关闭", 10, p.muted)
		})
	})
	if dialog.Dismissed() {
		s.closeMaterialsFullTextSearch(false)
	}
}

func (s *app) materialsSearchContent(c *ui.Context, p palette, state *materialsPickerState) {
	if state.searchQuery == "" {
		ui.Column(c).Gap(5).Children(func() {
			label(c, "输入文字，查找当前任务的任务资料", 12, p.text)
			label(c, "支持文本、代码和 PDF · 按原文匹配，忽略大小写", 11, p.muted)
		})
		return
	}
	if state.searchLoading {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			progress := state.searchProgress
			if progress.TotalFiles <= 0 {
				label(c, "正在准备搜索…", 12, p.text)
			} else {
				label(c, fmt.Sprintf("正在搜索 %d / %d 个文件…", progress.CompletedFiles, progress.TotalFiles), 12, p.text)
			}
			if progress.CurrentPath != nil {
				label(c, *progress.CurrentPath, 10, p.muted).Wrap()
			}
			if action(c, "materials-search-stop", "close", "停止搜索", p, false) {
				s.stopMaterialsSearch()
			}
		})
		return
	}
	if state.searchError != "" {
		ui.Column(c).Gap(8).Children(func() {
			label(c, "无法搜索任务资料", 12, p.amber).FontWeight(600)
			label(c, state.searchError, 11, p.muted).Wrap()
			if action(c, "materials-search-retry", "refresh", "重试搜索", p, false) {
				s.queueMaterialsSearch()
			}
		})
		return
	}
	if state.searchStopped {
		ui.Column(c).Gap(8).Children(func() {
			label(c, "搜索已停止", 12, p.text)
			label(c, "修改搜索文字，或重新搜索。", 11, p.muted)
			if action(c, "materials-search-retry", "refresh", "重新搜索", p, false) {
				s.queueMaterialsSearch()
			}
		})
		return
	}
	result := state.searchResult
	if result == nil {
		label(c, "正在准备搜索…", 12, p.muted)
		return
	}
	if len(result.Hits) == 0 {
		ui.Column(c).Gap(6).Children(func() {
			switch {
			case result.SearchedFiles == 0 && result.FailureCount > 0:
				label(c, "资料读取失败", 12, p.amber).FontWeight(600)
				label(c, "可搜索的资料均未读取成功。查看跳过原因后重试。", 11, p.muted).Wrap()
			case result.TotalFiles == 0:
				label(c, "此目录没有可搜索的文本、代码或 PDF 文件。", 12, p.muted).Wrap()
			default:
				label(c, fmt.Sprintf("已搜索 %d 个文件，没有找到匹配内容。试试更短的文字。", result.SearchedFiles), 12, p.muted).Wrap()
			}
		})
	} else {
		files := map[string]bool{}
		for _, hit := range result.Hits {
			files[hit.RelativePath] = true
		}
		prefix := ""
		if result.Truncated {
			prefix = "前 "
		}
		label(c, fmt.Sprintf("%s%d 处匹配 · %d 个文件", prefix, len(result.Hits), len(files)), 10, p.muted)
		ui.Scroll(c).Grow(1).MinHeight(100).Gap(2).Children(func() {
			for index, hit := range result.Hits {
				selected := index == state.searchSelected
				row := ui.ButtonBase(c.Key(fmt.Sprintf("materials-search-result-%d", index))).Column().FillWidth().Padding(8, 10).Gap(3).Radius(7).Label(hit.RelativePath)
				if selected {
					row.Background(p.selected)
				} else if row.Hovered() {
					row.Background(p.soft)
				}
				if selected {
					row.ScrollIntoView()
				}
				row.Children(func() {
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						label(c, hit.FileName, 12, p.text).FontWeight(500).Grow(1).SingleLine()
						label(c, searchHitLocation(hit), 10, p.muted).NoWrap()
					})
					if hit.RelativePath != hit.FileName {
						label(c, hit.RelativePath, 10, p.muted).SingleLine()
					}
					renderSearchSnippet(c, p, hit)
				})
				if row.Clicked() {
					state.searchSelected = index
					s.selectMaterialsSearchHit(hit)
				}
			}
		})
	}
	if result.Truncated {
		label(c, "已达到本次搜索上限。输入更具体的文字后继续查找。", 10, p.muted).Wrap()
	}
	if result.FailureCount > 0 {
		labelText := fmt.Sprintf("%d 项未能搜索 · %s原因", result.FailureCount, map[bool]string{true: "收起", false: "查看"}[state.searchFailures])
		if action(c, "materials-search-failures", "chevron", labelText, p, state.searchFailures) {
			state.searchFailures = !state.searchFailures
		}
		if state.searchFailures {
			ui.Scroll(c).Height(116).Gap(3).Children(func() {
				for _, failure := range result.Failures {
					path := failure.RelativePath
					if path == "" {
						path = "资料目录"
					}
					label(c, path+"："+failure.Message, 10, p.amber).Wrap()
				}
			})
		}
		if result.SearchedFiles == 0 && action(c, "materials-search-failures-retry", "refresh", "重试搜索", p, false) {
			s.queueMaterialsSearch()
		}
	}
}

func (s *app) moveMaterialsSearchSelection(state *materialsPickerState, delta int) {
	if state == nil || state.searchResult == nil || len(state.searchResult.Hits) == 0 {
		return
	}
	state.searchSelected = max(0, min(len(state.searchResult.Hits)-1, state.searchSelected+delta))
}

func (s *app) openSelectedMaterialsSearchHit(state *materialsPickerState) {
	if state == nil || state.searchResult == nil || state.searchSelected < 0 || state.searchSelected >= len(state.searchResult.Hits) {
		return
	}
	s.selectMaterialsSearchHit(state.searchResult.Hits[state.searchSelected])
}

func (s *app) selectMaterialsSearchHit(hit documents.SearchHit) {
	state := s.materialsUnit.ensureMaterialsPickerState()
	task := s.taskUnit.currentTask()
	if task.id == "" || task.id != state.searchTaskID || task.materialsPath == "" || s.materialsFileReadBlocked() {
		return
	}
	relative := filepath.FromSlash(hit.RelativePath)
	path := filepath.Clean(filepath.Join(task.materialsPath, relative))
	root, err := filepath.Abs(task.materialsPath)
	if err != nil {
		state.searchError = err.Error()
		return
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		state.searchError = "搜索结果路径超出当前任务资料目录。"
		return
	}
	// Selection changes documentPath before its asynchronous read succeeds.
	alreadyLoaded := path == s.materialsUnit.state.documentPath && !s.materialsUnit.state.documentReadPending &&
		(s.materialsUnit.state.documentContent != "" || s.materialsUnit.state.documentHash != "" || s.materialsUnit.state.documentKind == "pdf" && s.materialsUnit.state.nativeMedia != nil && s.materialsUnit.state.nativeMedia.path == path)
	copyHit := hit
	state.pendingHit = &copyHit
	state.pendingHitTask = task.id
	state.pendingHitPath = path
	s.closeMaterialsFullTextSearch(false)
	s.materialsUnit.state.documentFindOpen = true
	s.materialsUnit.state.documentSearch = hit.Query
	s.selectMaterialsPath(path)
	if alreadyLoaded {
		s.applyPendingMaterialsSearchHit(application.Action{Kind: "document.read", TaskID: task.id, Path: path}, application.ActionResult{Path: path, Data: map[string]any{"kind": s.materialsUnit.state.documentKind}})
		return
	}
	s.selectDocument(path)
}

// applyPendingMaterialsSearchHit is called after the regular, root-validated
// document.read result has opened the selected material in the native viewer.
func (s *app) applyPendingMaterialsSearchHit(action application.Action, result application.ActionResult) {
	state := s.materialsUnit.state.materialsPicker
	if state == nil || state.pendingHit == nil || action.Kind != "document.read" ||
		action.TaskID != state.pendingHitTask || s.taskUnit.currentTask().id != state.pendingHitTask {
		return
	}
	path := result.Path
	if path == "" {
		path = action.Path
	}
	if filepath.Clean(path) != filepath.Clean(state.pendingHitPath) || filepath.Clean(s.materialsUnit.state.documentPath) != filepath.Clean(state.pendingHitPath) {
		return
	}
	hit := *state.pendingHit
	state.pendingHit = nil
	state.pendingHitTask = ""
	state.pendingHitPath = ""
	s.materialsUnit.state.documentSearch = hit.Query
	s.materialsUnit.state.documentFindOpen = false
	if s.materialsUnit.state.documentKind != "pdf" || s.materialsUnit.state.nativeMedia == nil || s.materialsUnit.state.nativeMedia.path != path {
		state.sourcePreviewPath = path
		state.sourcePreviewHit = &hit
		state.sourcePreviewReveal = true
		s.materialsUnit.state.documentEditing = false
		return
	}
	state.sourcePreviewPath = ""
	state.sourcePreviewHit = nil
	state.sourcePreviewReveal = false
	m := s.materialsUnit.state.nativeMedia
	m.showPDFText = true
	m.search = hit.Query
	if hit.PageIndex != nil {
		s.navigatePDFPage(m, *hit.PageIndex)
	}
	s.requestMaterialsPDFHit(m, hit)
}

func utf16ToRuneOffset(value string, offset int) int {
	if offset <= 0 {
		return 0
	}
	runes := []rune(value)
	units := 0
	for index, current := range runes {
		units += len(utf16.Encode([]rune{current}))
		if units >= offset {
			return index + 1
		}
	}
	return len(runes)
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func searchHitLocation(hit documents.SearchHit) string {
	if hit.PageIndex != nil {
		return fmt.Sprintf("第 %d 页", *hit.PageIndex+1)
	}
	if hit.Line != nil {
		return fmt.Sprintf("第 %d 行", *hit.Line)
	}
	return "资料"
}

func renderSearchSnippet(c *ui.Context, p palette, hit documents.SearchHit) {
	runes := []rune(hit.Snippet)
	start := utf16ToRuneStart(hit.Snippet, hit.SnippetMatchStart)
	end := utf16ToRuneStart(hit.Snippet, hit.SnippetMatchEnd)
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if end > len(runes) {
		end = len(runes)
	}
	spans := make([]ui.Span, 0, 3)
	if start > 0 {
		spans = append(spans, ui.Span{Text: string(runes[:start])})
	}
	if end > start {
		spans = append(spans, ui.Span{Text: string(runes[start:end]), Color: p.blue, Weight: 600, Background: p.selected})
	}
	if end < len(runes) {
		spans = append(spans, ui.Span{Text: string(runes[end:])})
	}
	ui.RichText(c, spans...).FontSize(11).TextColor(p.muted).Wrap()
}

func utf16ToRuneStart(value string, offset int) int {
	if offset <= 0 {
		return 0
	}
	units := 0
	for index, current := range []rune(value) {
		width := len(utf16.Encode([]rune{current}))
		if units+width > offset {
			return index
		}
		units += width
		if units >= offset {
			return index + 1
		}
	}
	return len([]rune(value))
}

// renderMaterialsSearchSourcePreview shows the raw read-only source for the
// selected full-text result, matching the Kotlin reader's SOURCE mode. The
// active occurrence is selected so MyGO scrolls it into view; TextRanges
// styles the other occurrences in the file.
func (s *app) renderMaterialsSearchSourcePreview(c *ui.Context, p palette) bool {
	state := s.materialsUnit.state.materialsPicker
	if state == nil || state.sourcePreviewHit == nil || state.sourcePreviewPath == "" ||
		filepath.Clean(state.sourcePreviewPath) != filepath.Clean(s.materialsUnit.state.documentPath) || s.materialsUnit.state.documentKind == "pdf" {
		return false
	}
	hit := *state.sourcePreviewHit
	content := s.materialsUnit.state.documentContent
	field := ui.TextArea(c.Key("materials-search-source-"+state.sourcePreviewPath), &content).
		FillWidth().Height(560).Padding(14).Font("Consolas").FontSize(13).
		Background(p.surface).Border(1, p.line).Radius(8).ReadOnly(true).
		TextRanges(materialsSearchTextRanges(content, hit.Query, p.blue)...)
	if state.sourcePreviewReveal {
		start := utf16OffsetToRune(content, hit.Offset)
		end := utf16OffsetToRune(content, hit.Offset+len(utf16.Encode([]rune(hit.Query))))
		field.Focus().SetTextSelection(start, end)
		state.sourcePreviewReveal = false
	}
	return true
}

func materialsSearchTextRanges(text, query string, color ui.Color) []ui.TextRange {
	textRunes, queryRunes := []rune(text), []rune(query)
	if len(queryRunes) == 0 || len(textRunes) < len(queryRunes) {
		return nil
	}
	folded := make([]rune, len(queryRunes))
	for i, r := range queryRunes {
		folded[i] = materialsFoldRune(r)
	}
	prefix := make([]int, len(folded))
	for i, matched := 1, 0; i < len(folded); i++ {
		for matched > 0 && folded[i] != folded[matched] {
			matched = prefix[matched-1]
		}
		if folded[i] == folded[matched] {
			matched++
		}
		prefix[i] = matched
	}
	ranges := make([]ui.TextRange, 0, min(64, len(textRunes)/max(1, len(queryRunes))))
	matched := 0
	for index, r := range textRunes {
		current := materialsFoldRune(r)
		for matched > 0 && current != folded[matched] {
			matched = prefix[matched-1]
		}
		if current == folded[matched] {
			matched++
		}
		if matched == len(folded) {
			start := index + 1 - len(folded)
			ranges = append(ranges, ui.TextRange{Start: start, End: index + 1, Color: color, Weight: 600})
			matched = 0 // Match Source's non-overlapping find results.
			if len(ranges) >= 10_000 {
				break
			}
		}
	}
	return ranges
}

func materialsFoldRune(value rune) rune {
	minimum := value
	for next := unicode.SimpleFold(value); next != value; next = unicode.SimpleFold(next) {
		if next < minimum {
			minimum = next
		}
	}
	return minimum
}
