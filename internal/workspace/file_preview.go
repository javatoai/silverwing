package workspace

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"silverwing-go/internal/domain"
)

const MaxWorkspaceFilePreviewBytes = 512 * 1024

type WorkspaceFileLanguage string

const (
	FileLanguageJava       WorkspaceFileLanguage = "JAVA"
	FileLanguageKotlin     WorkspaceFileLanguage = "KOTLIN"
	FileLanguageGradle     WorkspaceFileLanguage = "GRADLE"
	FileLanguageProperties WorkspaceFileLanguage = "PROPERTIES"
	FileLanguageHTML       WorkspaceFileLanguage = "HTML"
	FileLanguageCSS        WorkspaceFileLanguage = "CSS"
	FileLanguageJavaScript WorkspaceFileLanguage = "JAVASCRIPT"
	FileLanguageTypeScript WorkspaceFileLanguage = "TYPESCRIPT"
	FileLanguageJSON       WorkspaceFileLanguage = "JSON"
	FileLanguageYAML       WorkspaceFileLanguage = "YAML"
	FileLanguageSQL        WorkspaceFileLanguage = "SQL"
	FileLanguagePython     WorkspaceFileLanguage = "PYTHON"
	FileLanguageShell      WorkspaceFileLanguage = "SHELL"
	FileLanguagePowerShell WorkspaceFileLanguage = "POWERSHELL"
	FileLanguageBatch      WorkspaceFileLanguage = "BATCH"
	FileLanguageMarkdown   WorkspaceFileLanguage = "MARKDOWN"
	FileLanguagePlainText  WorkspaceFileLanguage = "PLAIN_TEXT"
)

func WorkspaceFileLanguageFromPath(path string) WorkspaceFileLanguage {
	p := strings.ToLower(path)
	switch {
	case strings.HasSuffix(p, ".gradle"), strings.HasSuffix(p, ".gradle.kts"):
		return FileLanguageGradle
	case strings.HasSuffix(p, ".java"):
		return FileLanguageJava
	case strings.HasSuffix(p, ".kt"), strings.HasSuffix(p, ".kts"):
		return FileLanguageKotlin
	case strings.HasSuffix(p, ".properties"), strings.HasSuffix(p, ".ini"), strings.HasSuffix(p, ".conf"):
		return FileLanguageProperties
	case strings.HasSuffix(p, ".html"), strings.HasSuffix(p, ".htm"), strings.HasSuffix(p, ".xml"), strings.HasSuffix(p, ".svg"):
		return FileLanguageHTML
	case strings.HasSuffix(p, ".css"), strings.HasSuffix(p, ".scss"), strings.HasSuffix(p, ".sass"), strings.HasSuffix(p, ".less"):
		return FileLanguageCSS
	case strings.HasSuffix(p, ".js"), strings.HasSuffix(p, ".jsx"):
		return FileLanguageJavaScript
	case strings.HasSuffix(p, ".ts"), strings.HasSuffix(p, ".tsx"):
		return FileLanguageTypeScript
	case strings.HasSuffix(p, ".json"):
		return FileLanguageJSON
	case strings.HasSuffix(p, ".yaml"), strings.HasSuffix(p, ".yml"):
		return FileLanguageYAML
	case strings.HasSuffix(p, ".sql"):
		return FileLanguageSQL
	case strings.HasSuffix(p, ".py"), strings.HasSuffix(p, ".pyw"), strings.HasSuffix(p, ".pyi"):
		return FileLanguagePython
	case strings.HasSuffix(p, ".ps1"), strings.HasSuffix(p, ".psm1"), strings.HasSuffix(p, ".psd1"):
		return FileLanguagePowerShell
	case strings.HasSuffix(p, ".bat"), strings.HasSuffix(p, ".cmd"):
		return FileLanguageBatch
	case strings.HasSuffix(p, ".sh"), strings.HasSuffix(p, ".bash"), strings.HasSuffix(p, ".zsh"):
		return FileLanguageShell
	case strings.HasSuffix(p, ".md"), strings.HasSuffix(p, ".markdown"):
		return FileLanguageMarkdown
	default:
		return FileLanguagePlainText
	}
}

type WorkspaceFilePreviewMode string

const (
	PreviewComparison WorkspaceFilePreviewMode = "COMPARISON"
	PreviewContent    WorkspaceFilePreviewMode = "CONTENT"
)

type WorkspaceFileContentOrigin string

const (
	PreviewOriginWorktree        WorkspaceFileContentOrigin = "WORKTREE"
	PreviewOriginHead            WorkspaceFileContentOrigin = "HEAD"
	PreviewOriginMainline        WorkspaceFileContentOrigin = "MAINLINE"
	PreviewOriginBaseline        WorkspaceFileContentOrigin = "BASELINE"
	PreviewOriginSelectedCommits WorkspaceFileContentOrigin = "SELECTED_COMMITS"
)

type WorkspaceFileComparisonLineKind string

const (
	PreviewContext WorkspaceFileComparisonLineKind = "CONTEXT"
	PreviewDeleted WorkspaceFileComparisonLineKind = "DELETED"
	PreviewAdded   WorkspaceFileComparisonLineKind = "ADDED"
)

type WorkspaceFilePreviewContent struct {
	Text      string                     `json:"text"`
	Truncated bool                       `json:"truncated"`
	Origin    WorkspaceFileContentOrigin `json:"origin"`
}
type WorkspaceFileComparisonLine struct {
	Kind       WorkspaceFileComparisonLineKind `json:"kind"`
	LineNumber int                             `json:"lineNumber"`
	Text       string                          `json:"text"`
}
type WorkspaceFileComparisonRow struct {
	OldLine *WorkspaceFileComparisonLine `json:"oldLine"`
	NewLine *WorkspaceFileComparisonLine `json:"newLine"`
}
type WorkspaceFileComparison struct {
	Rows       []WorkspaceFileComparisonRow `json:"rows"`
	OldContent *WorkspaceFilePreviewContent `json:"oldContent"`
	NewContent *WorkspaceFilePreviewContent `json:"newContent"`
	Truncated  bool                         `json:"truncated"`
}
type WorkspaceGitFilePreview struct {
	Language          WorkspaceFileLanguage        `json:"language"`
	Content           *WorkspaceFilePreviewContent `json:"content"`
	Comparison        *WorkspaceFileComparison     `json:"comparison"`
	Notice            string                       `json:"notice"`
	UnavailableReason string                       `json:"unavailableReason"`
}

func (p WorkspaceGitFilePreview) DefaultMode() WorkspaceFilePreviewMode {
	if p.Comparison != nil && len(p.Comparison.Rows) > 0 {
		return PreviewComparison
	}
	return PreviewContent
}

type WorkspaceGitFilePreviewService struct{ Git *GitClient }

func NewWorkspaceGitFilePreviewService(git *GitClient) *WorkspaceGitFilePreviewService {
	if git == nil {
		git = NewGitClient(nil, "")
	}
	return &WorkspaceGitFilePreviewService{Git: git}
}

func (s *WorkspaceGitFilePreviewService) Preview(worktreePath string, change domain.WorkspaceGitFileChange) (WorkspaceGitFilePreview, error) {
	return s.PreviewContext(context.Background(), worktreePath, change)
}

func (s *WorkspaceGitFilePreviewService) PreviewContext(ctx context.Context, worktreePath string, change domain.WorkspaceGitFileChange) (WorkspaceGitFilePreview, error) {
	if err := ctx.Err(); err != nil {
		return WorkspaceGitFilePreview{}, err
	}
	root, err := filepath.Abs(filepath.Clean(worktreePath))
	if err != nil {
		return WorkspaceGitFilePreview{}, err
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return WorkspaceGitFilePreview{}, fmt.Errorf("工作区不存在，无法预览文件")
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return WorkspaceGitFilePreview{}, fmt.Errorf("工作区不存在，无法预览文件")
	}
	relative, err := normalizePreviewPath(root, change.Path)
	if err != nil {
		return WorkspaceGitFilePreview{}, err
	}
	change.Path = relative
	base := WorkspaceGitFilePreview{Language: WorkspaceFileLanguageFromPath(relative)}
	readCurrent := func() (previewRead, error) {
		if err := ctx.Err(); err != nil {
			return previewRead{}, err
		}
		return readPreviewFile(root, realRoot, relative)
	}
	readHead := func() (previewRead, error) { return s.readHead(ctx, root, relative) }
	readDiff := func() diffRead { return s.readDiff(ctx, root, relative) }
	switch change.Kind {
	case domain.WorkspaceGitModified:
		old, _ := readHead()
		if err := ctx.Err(); err != nil {
			return WorkspaceGitFilePreview{}, err
		}
		current, _ := readCurrent()
		if err := ctx.Err(); err != nil {
			return WorkspaceGitFilePreview{}, err
		}
		diff := readDiff()
		if err := ctx.Err(); err != nil {
			return WorkspaceGitFilePreview{}, err
		}
		base.Content = current.content
		base.Comparison = buildFileComparison(old.content, current.content, diff.hunks)
		base.Notice = comparisonNotice(base.Comparison, old, current, diff)
		base.UnavailableReason = firstNonEmpty(current.reason, old.reason, diff.reason)
	case domain.WorkspaceGitAdded, domain.WorkspaceGitUntracked:
		current, _ := readCurrent()
		base.Content = current.content
		base.Comparison = singleFileComparison(current.content, PreviewAdded)
		base.Notice = comparisonNotice(base.Comparison, previewRead{}, current, diffRead{})
		base.UnavailableReason = current.reason
	case domain.WorkspaceGitDeleted:
		old, _ := readHead()
		if err := ctx.Err(); err != nil {
			return WorkspaceGitFilePreview{}, err
		}
		base.Content = old.content
		base.Comparison = singleFileComparison(old.content, PreviewDeleted)
		base.Notice = comparisonNotice(base.Comparison, old, previewRead{}, diffRead{})
		base.UnavailableReason = old.reason
	case domain.WorkspaceGitRenamed:
		current, _ := readCurrent()
		base.Content, base.UnavailableReason = current.content, current.reason
		base.Notice = "文件已重命名，显示当前文件内容。"
	case domain.WorkspaceGitCopied:
		current, _ := readCurrent()
		base.Content, base.UnavailableReason = current.content, current.reason
		base.Notice = "文件已复制，显示当前文件内容。"
	case domain.WorkspaceGitTypeChanged:
		current, _ := readCurrent()
		base.Content, base.UnavailableReason = current.content, current.reason
		base.Notice = "文件类型已变更，显示当前文件内容。"
	case domain.WorkspaceGitConflicted:
		current, _ := readCurrent()
		base.Content, base.UnavailableReason = current.content, current.reason
		base.Notice = "文件存在冲突，显示当前文件内容。"
	default:
		return WorkspaceGitFilePreview{}, fmt.Errorf("不支持的 Git 文件状态：%s", change.Kind)
	}
	if err := ctx.Err(); err != nil {
		return WorkspaceGitFilePreview{}, err
	}
	return base, nil
}

type previewRead struct {
	content *WorkspaceFilePreviewContent
	reason  string
}
type diffHunk struct {
	oldStart, newStart int
	ops                []byte
}
type diffRead struct {
	hunks  []diffHunk
	reason string
}

func normalizePreviewPath(root, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("文件路径不能为空")
	}
	target := filepath.Clean(filepath.Join(root, filepath.FromSlash(strings.ReplaceAll(path, "\\", "/"))))
	if !isWithin(target, root) {
		return "", fmt.Errorf("文件路径不在当前工作区内，无法预览")
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(relative), nil
}

func readPreviewFile(root, realRoot, relative string) (previewRead, error) {
	target := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return previewRead{reason: "文件不存在或已删除，无法预览"}, nil
	}
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		return previewRead{reason: "文件不存在或已删除，无法预览"}, nil
	}
	if !isWithin(real, realRoot) {
		return previewRead{reason: "文件不在当前工作区内，无法预览"}, nil
	}
	file, err := os.Open(real)
	if err != nil {
		return previewRead{reason: err.Error()}, nil
	}
	defer file.Close()
	data, err := readAtMost(file, MaxWorkspaceFilePreviewBytes+1)
	if err != nil {
		return previewRead{reason: err.Error()}, nil
	}
	return decodePreview(data, PreviewOriginWorktree)
}

func readAtMost(file *os.File, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(file, limit))
}

func (s *WorkspaceGitFilePreviewService) readHead(ctx context.Context, root, relative string) (previewRead, error) {
	result, err := s.Git.RunContext(ctx, root, 2*time.Minute, false, true, "cat-file", "blob", "HEAD:"+relative)
	if err != nil {
		return previewRead{reason: "无法从 HEAD 读取文件"}, err
	}
	if result.ExitCode != 0 {
		return previewRead{reason: "无法从 HEAD 读取文件"}, nil
	}
	data := []byte(result.Stdout)
	// Command output may already contain replacement runes for invalid UTF-8.
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) || bytes.ContainsRune(data, utf8.RuneError) {
		return previewRead{reason: "疑似二进制文件，暂不支持文本预览"}, nil
	}
	content := string(data)
	truncated := len(data) > MaxWorkspaceFilePreviewBytes
	if truncated {
		content = truncatePreview(content)
	}
	return previewRead{content: &WorkspaceFilePreviewContent{Text: content, Truncated: truncated, Origin: PreviewOriginHead}}, nil
}

func decodePreview(data []byte, origin WorkspaceFileContentOrigin) (previewRead, error) {
	truncated := len(data) > MaxWorkspaceFilePreviewBytes
	if truncated {
		data = data[:MaxWorkspaceFilePreviewBytes]
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return previewRead{reason: "疑似二进制文件，暂不支持文本预览"}, nil
	}
	text := string(data)
	if !utf8.Valid(data) && truncated {
		for trim := 1; trim <= 3 && trim < len(data); trim++ {
			candidate := data[:len(data)-trim]
			if utf8.Valid(candidate) {
				text = string(candidate)
				break
			}
		}
	}
	if !utf8.ValidString(text) {
		return previewRead{reason: "文件不是 UTF-8 文本，暂不支持预览"}, nil
	}
	return previewRead{content: &WorkspaceFilePreviewContent{Text: text, Truncated: truncated, Origin: origin}}, nil
}

func (s *WorkspaceGitFilePreviewService) readDiff(ctx context.Context, root, relative string) diffRead {
	result, err := s.Git.RunContext(ctx, root, 2*time.Minute, false, true, "diff", "--no-ext-diff", "--unified=0", "HEAD", "--", relative)
	if err != nil || result.ExitCode != 0 {
		return diffRead{reason: "无法读取 Git 差异"}
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return diffRead{hunks: []diffHunk{}}
	}
	if strings.Contains(result.Stdout, "GIT binary patch") || strings.Contains(result.Stdout, "Binary files ") {
		return diffRead{reason: "疑似二进制文件，无法显示左右对比"}
	}
	if len(result.Stdout) > MaxWorkspaceFilePreviewBytes {
		return diffRead{reason: "变更内容较大，无法生成完整左右对比"}
	}
	return diffRead{hunks: parseDiffHunks(result.Stdout)}
}

var diffHunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@.*$`)

func parseDiffHunks(output string) []diffHunk {
	var result []diffHunk
	var current *diffHunk
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if match := diffHunkHeader.FindStringSubmatch(line); match != nil {
			if current != nil {
				result = append(result, *current)
			}
			oldStart, _ := strconv.Atoi(match[1])
			newStart, _ := strconv.Atoi(match[2])
			current = &diffHunk{oldStart: oldStart, newStart: newStart}
			continue
		}
		if current == nil || line == `\ No newline at end of file` {
			continue
		}
		if strings.HasPrefix(line, "-") {
			current.ops = append(current.ops, '-')
		} else if strings.HasPrefix(line, "+") {
			current.ops = append(current.ops, '+')
		}
	}
	if current != nil {
		result = append(result, *current)
	}
	return result
}

func buildFileComparison(old, current *WorkspaceFilePreviewContent, hunks []diffHunk) *WorkspaceFileComparison {
	if old == nil || current == nil || hunks == nil || old.Truncated || current.Truncated {
		return nil
	}
	oldLines, newLines := previewLines(old.Text), previewLines(current.Text)
	rows := []WorkspaceFileComparisonRow{}
	oi, ni := 0, 0
	appendContext := func(oldTarget, newTarget int) bool {
		if oldTarget < oi || oldTarget > len(oldLines) || newTarget < ni || newTarget > len(newLines) {
			return false
		}
		for oi < oldTarget || ni < newTarget {
			var left, right *WorkspaceFileComparisonLine
			if oi < len(oldLines) {
				line := WorkspaceFileComparisonLine{Kind: PreviewContext, LineNumber: oi + 1, Text: oldLines[oi]}
				left = &line
			}
			if ni < len(newLines) {
				line := WorkspaceFileComparisonLine{Kind: PreviewContext, LineNumber: ni + 1, Text: newLines[ni]}
				right = &line
			}
			if left == nil && right == nil {
				return false
			}
			rows = append(rows, WorkspaceFileComparisonRow{OldLine: left, NewLine: right})
			if left != nil {
				oi++
			}
			if right != nil {
				ni++
			}
		}
		return true
	}
	for _, hunk := range hunks {
		oldTarget, newTarget := hunk.oldStart-1, hunk.newStart-1
		if hunk.oldStart == 0 {
			oldTarget = 0
		}
		if hunk.newStart == 0 {
			newTarget = 0
		}
		if !appendContext(oldTarget, newTarget) {
			return nil
		}
		deleted, added := []*WorkspaceFileComparisonLine{}, []*WorkspaceFileComparisonLine{}
		for _, op := range hunk.ops {
			if op == '-' {
				if oi >= len(oldLines) {
					return nil
				}
				line := WorkspaceFileComparisonLine{Kind: PreviewDeleted, LineNumber: oi + 1, Text: oldLines[oi]}
				deleted = append(deleted, &line)
				oi++
			}
			if op == '+' {
				if ni >= len(newLines) {
					return nil
				}
				line := WorkspaceFileComparisonLine{Kind: PreviewAdded, LineNumber: ni + 1, Text: newLines[ni]}
				added = append(added, &line)
				ni++
			}
		}
		count := max(len(deleted), len(added))
		for i := 0; i < count; i++ {
			var left, right *WorkspaceFileComparisonLine
			if i < len(deleted) {
				left = deleted[i]
			}
			if i < len(added) {
				right = added[i]
			}
			rows = append(rows, WorkspaceFileComparisonRow{OldLine: left, NewLine: right})
		}
	}
	if !appendContext(len(oldLines), len(newLines)) {
		return nil
	}
	return &WorkspaceFileComparison{Rows: rows, OldContent: old, NewContent: current}
}

func singleFileComparison(content *WorkspaceFilePreviewContent, kind WorkspaceFileComparisonLineKind) *WorkspaceFileComparison {
	if content == nil || content.Truncated {
		return nil
	}
	rows := make([]WorkspaceFileComparisonRow, 0)
	for index, text := range previewLines(content.Text) {
		line := WorkspaceFileComparisonLine{Kind: kind, LineNumber: index + 1, Text: text}
		if kind == PreviewDeleted {
			rows = append(rows, WorkspaceFileComparisonRow{OldLine: &line})
		} else {
			rows = append(rows, WorkspaceFileComparisonRow{NewLine: &line})
		}
	}
	comparison := &WorkspaceFileComparison{Rows: rows}
	if kind == PreviewDeleted {
		comparison.OldContent = content
	} else {
		comparison.NewContent = content
	}
	return comparison
}

func comparisonNotice(comparison *WorkspaceFileComparison, old, current previewRead, diff diffRead) string {
	if comparison != nil {
		return ""
	}
	if (old.content != nil && old.content.Truncated) || (current.content != nil && current.content.Truncated) {
		return "文件较大，无法生成完整左右对比，已显示可读取的文件内容。"
	}
	if diff.reason != "" {
		return "无法读取 Git 差异，已显示可读取的文件内容。"
	}
	if old.content != nil || current.content != nil {
		return "无法生成完整左右对比，已显示可读取的文件内容。"
	}
	return ""
}

func truncatePreview(text string) string {
	if len(text) <= MaxWorkspaceFilePreviewBytes {
		return text
	}
	end := strings.LastIndex(text[:MaxWorkspaceFilePreviewBytes], "\n")
	if end <= 0 {
		end = MaxWorkspaceFilePreviewBytes
	}
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}
func previewLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
