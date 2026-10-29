package documents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"silverwing-go/internal/media"
)

// These bounds match MaterialsFullTextSearchLimits in the Kotlin desktop.
const (
	maxMaterialsSearchResults      = 500
	maxMaterialsSearchFiles        = 5_000
	maxMaterialsSearchEntries      = 20_000
	maxMaterialsSearchTextBytes    = 512 * 1024
	maxMaterialsSearchPDFBytes     = 64 * 1024 * 1024
	maxMaterialsSearchTotalBytes   = 256 * 1024 * 1024
	maxMaterialsSearchFailureItems = 100
	maxMaterialsSearchQueryUTF16   = 4_096
)

type SearchHit struct {
	RelativePath        string `json:"relativePath"`
	FileName            string `json:"fileName"`
	Query               string `json:"query"`
	Snippet             string `json:"snippet"`
	SnippetMatchStart   int    `json:"snippetMatchStart"`
	SnippetMatchEnd     int    `json:"snippetMatchEnd"`
	Offset              int    `json:"offset"`
	Line                *int   `json:"line"`
	Column              *int   `json:"column"`
	PageIndex           *int   `json:"pageIndex"`
	OccurrenceIndex     int    `json:"occurrenceIndex"`
	PageOccurrenceIndex *int   `json:"pageOccurrenceIndex"`
}

type SearchFailure struct {
	RelativePath string `json:"relativePath"`
	Message      string `json:"message"`
}

type MaterialsSearchProgress struct {
	CompletedFiles int     `json:"completedFiles"`
	TotalFiles     int     `json:"totalFiles"`
	CurrentPath    *string `json:"currentPath"`
}

type materialsSearchProgressKey struct{}

// WithMaterialsSearchProgress attaches a per-search progress observer. The
// observer lives in context rather than Action.Payload, which remains JSON-safe.
func WithMaterialsSearchProgress(ctx context.Context, observer func(MaterialsSearchProgress)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, materialsSearchProgressKey{}, observer)
}

type SearchResult struct {
	Query            string          `json:"query"`
	Hits             []SearchHit     `json:"hits"`
	SearchedFiles    int             `json:"searchedFiles"`
	TotalFiles       int             `json:"totalFiles"`
	SkippedFiles     int             `json:"skippedFiles"`
	Failures         []SearchFailure `json:"failures"`
	Truncated        bool            `json:"truncated"`
	FailureCount     int             `json:"failureCount"`
	UnsupportedFiles int             `json:"unsupportedFiles"`
	ReadBytes        int64           `json:"readBytes"`
}

type searchCandidate struct {
	relative string
	size     int64
	pdf      bool
}

// SearchMaterials searches only the current task's materials directory. Paths
// are revalidated against the task root before each read, and progress is
// reported by the caller without an HTTP or process boundary.
func (s *Service) SearchMaterials(ctx context.Context, root, query string) (result SearchResult, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if utf16Length(query) > maxMaterialsSearchQueryUTF16 {
		return SearchResult{}, errors.New("搜索文字过长，请缩短到 4096 个字符以内。")
	}
	result = SearchResult{Query: query, Hits: []SearchHit{}, Failures: []SearchFailure{}}
	onProgress, _ := ctx.Value(materialsSearchProgressKey{}).(func(MaterialsSearchProgress))
	if query == "" {
		return result, nil
	}
	scope, err := newMaterialsOperationScope(root)
	if err != nil {
		return SearchResult{}, err
	}
	defer func() {
		if !scope.finish(&resultErr) {
			result = SearchResult{}
		}
	}()
	validatedRoot := scope.root
	rootInfo, err := os.Lstat(validatedRoot)
	if err != nil {
		return SearchResult{}, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() || !sameResolvedPath(validatedRoot) {
		return SearchResult{}, errors.New("任务资料目录不存在或无法读取，请刷新目录后重试。")
	}

	candidates := make([]searchCandidate, 0, 128)
	entryCount := 0
	truncated := false
	result.UnsupportedFiles = 0
	addFailure := func(relative, message string) {
		result.FailureCount++
		if len(result.Failures) < maxMaterialsSearchFailureItems {
			result.Failures = append(result.Failures, SearchFailure{RelativePath: relative, Message: message})
		}
	}
	visit := func(relative string) bool {
		if err := ctx.Err(); err != nil {
			return false
		}
		entryCount++
		if entryCount > maxMaterialsSearchEntries {
			truncated = true
			return false
		}
		return true
	}
	walkErr := filepath.WalkDir(validatedRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		relative, relErr := filepath.Rel(validatedRoot, path)
		if relErr != nil {
			return relErr
		}
		if relative == "." {
			relative = ""
		} else {
			relative = filepath.ToSlash(relative)
		}
		if !visit(relative) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fs.SkipAll
		}
		if walkErr != nil {
			addFailure(relative, "无法读取此项："+walkErr.Error())
			return nil
		}
		if relative == "" {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if strings.EqualFold(name, ".git") || strings.HasPrefix(strings.ToLower(name), ".silverwing-") || stagingName.MatchString(name) {
				return filepath.SkipDir
			}
			info, err := os.Lstat(path)
			if err != nil {
				addFailure(relative, "无法读取目录："+err.Error())
				return filepath.SkipDir
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !sameResolvedPath(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if stagingName.MatchString(name) || strings.HasPrefix(strings.ToLower(name), ".silverwing-") {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			addFailure(relative, "无法读取此项："+err.Error())
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !sameResolvedPath(path) {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		isPDF := ext == ".pdf"
		if !isPDF && !searchableTextExtension(name) {
			result.UnsupportedFiles++
			return nil
		}
		if len(candidates) == maxMaterialsSearchFiles {
			truncated = true
			return fs.SkipAll
		}
		candidates = append(candidates, searchCandidate{relative: relative, size: info.Size(), pdf: isPDF})
		return nil
	})
	if err := ctx.Err(); err != nil {
		return SearchResult{}, err
	}
	if walkErr != nil && !errors.Is(walkErr, fs.SkipAll) {
		return SearchResult{}, walkErr
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := strings.ToLower(candidates[i].relative), strings.ToLower(candidates[j].relative)
		if left != right {
			return left < right
		}
		return candidates[i].relative < candidates[j].relative
	})
	result.TotalFiles = len(candidates)
	result.Truncated = truncated
	if onProgress != nil {
		onProgress(MaterialsSearchProgress{CompletedFiles: 0, TotalFiles: len(candidates)})
	}

	for _, candidate := range candidates {
		if err := scope.revalidate(); err != nil {
			return SearchResult{}, err
		}
		if err := ctx.Err(); err != nil {
			return SearchResult{}, err
		}
		if len(result.Hits) == maxMaterialsSearchResults || result.ReadBytes >= maxMaterialsSearchTotalBytes {
			result.Truncated = true
			break
		}
		if onProgress != nil {
			current := candidate.relative
			onProgress(MaterialsSearchProgress{CompletedFiles: result.SearchedFiles + result.SkippedFiles, TotalFiles: result.TotalFiles, CurrentPath: &current})
		}
		limit := int64(maxMaterialsSearchTextBytes)
		if candidate.pdf {
			limit = maxMaterialsSearchPDFBytes
		}
		remaining := min64(maxMaterialsSearchTotalBytes-result.ReadBytes, limit)
		if candidate.size > remaining {
			result.SkippedFiles++
			message := "文件超过搜索大小上限，已跳过。"
			if candidate.size <= limit {
				message = "本次读取总量达到上限，已跳过。"
				result.Truncated = true
			}
			addFailure(candidate.relative, message)
			continue
		}
		path, err := safePath(validatedRoot, filepath.FromSlash(candidate.relative), false)
		if err != nil {
			result.SkippedFiles++
			addFailure(candidate.relative, "无法读取此项："+err.Error())
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !sameResolvedPath(path) {
			result.SkippedFiles++
			if err == nil {
				err = errors.New("资料文件在搜索期间发生变化")
			}
			addFailure(candidate.relative, "无法读取此项："+err.Error())
			continue
		}
		if info.Size() > remaining {
			result.SkippedFiles++
			addFailure(candidate.relative, "文件在搜索期间增大，超过读取上限，已跳过。")
			result.Truncated = true
			continue
		}
		if candidate.pdf {
			pdf, openErr := media.OpenPDF(ctx, path, media.Options{MaxPDFBytes: remaining})
			if openErr != nil {
				if ctx.Err() != nil {
					return SearchResult{}, ctx.Err()
				}
				result.SkippedFiles++
				addFailure(candidate.relative, pdfSearchError(openErr))
				continue
			}
			index, indexErr := pdf.TextIndex(ctx)
			closeErr := pdf.Close()
			result.ReadBytes += info.Size()
			if indexErr == nil {
				indexErr = closeErr
			}
			if indexErr != nil {
				if ctx.Err() != nil {
					return SearchResult{}, ctx.Err()
				}
				result.SkippedFiles++
				addFailure(candidate.relative, pdfSearchError(indexErr))
				continue
			}
			occurrence := 0
			for pageNumber, page := range index.Pages {
				if err := ctx.Err(); err != nil {
					return SearchResult{}, err
				}
				pageIndex := pageNumber
				found := scanSearchText(candidate.relative, filepath.Base(path), query, page.Text, &pageIndex, occurrence, maxMaterialsSearchResults-len(result.Hits)+1)
				for i := range found {
					pageOccurrence := i
					found[i].PageOccurrenceIndex = &pageOccurrence
				}
				occurrence += len(found)
				if len(found)+len(result.Hits) > maxMaterialsSearchResults {
					result.Truncated = true
				}
				remainingHits := maxMaterialsSearchResults - len(result.Hits)
				if len(found) > remainingHits {
					found = found[:remainingHits]
				}
				result.Hits = append(result.Hits, found...)
				if result.Truncated && len(result.Hits) == maxMaterialsSearchResults {
					break
				}
			}
			result.SearchedFiles++
			continue
		}
		data, readErr := readSearchFile(ctx, path, remaining)
		result.ReadBytes += int64(len(data))
		if readErr != nil {
			if ctx.Err() != nil {
				return SearchResult{}, ctx.Err()
			}
			result.SkippedFiles++
			addFailure(candidate.relative, readErr.Error())
			continue
		}
		if !utf8.Valid(data) {
			result.SkippedFiles++
			addFailure(candidate.relative, "文件不是有效的 UTF-8 文本，已跳过。")
			continue
		}
		text := strings.TrimPrefix(string(data), "\uFEFF")
		found := scanSearchText(candidate.relative, filepath.Base(path), query, text, nil, 0, maxMaterialsSearchResults-len(result.Hits)+1)
		if len(found)+len(result.Hits) > maxMaterialsSearchResults {
			result.Truncated = true
		}
		remainingHits := maxMaterialsSearchResults - len(result.Hits)
		if len(found) > remainingHits {
			found = found[:remainingHits]
		}
		result.Hits = append(result.Hits, found...)
		result.SearchedFiles++
	}
	if err := ctx.Err(); err != nil {
		return SearchResult{}, err
	}
	if onProgress != nil {
		onProgress(MaterialsSearchProgress{CompletedFiles: result.SearchedFiles + result.SkippedFiles, TotalFiles: result.TotalFiles})
	}
	return result, nil
}

func readSearchFile(ctx context.Context, path string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取此项：%w", err)
	}
	file, err := openMaterialFileNoFollow(path, info)
	if err != nil {
		return nil, fmt.Errorf("无法读取此项：%w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return data, fmt.Errorf("无法读取此项：%w", err)
	}
	if int64(len(data)) > limit {
		return data, errors.New("文件在搜索期间增大，超过读取上限，已跳过。")
	}
	if err := ctx.Err(); err != nil {
		return data, err
	}
	return data, nil
}

func pdfSearchError(err error) string {
	message := err.Error()
	if strings.Contains(strings.ToLower(message), "password") || strings.Contains(message, "密码") {
		return "PDF 需要密码，请使用外部阅读器打开。"
	}
	if strings.Contains(message, "PDF 预览不可用") || strings.Contains(message, "PDFium") {
		return "PDF 文本索引不可用：" + message
	}
	return "PDF 无法读取或提取文字，文件可能已损坏：" + message
}

func searchableTextExtension(name string) bool {
	name = strings.ToLower(name)
	if strings.HasSuffix(name, ".txt") || strings.HasSuffix(name, ".csv") {
		return true
	}
	for _, extension := range []string{
		".gradle", ".gradle.kts", ".java", ".kt", ".kts", ".properties", ".ini", ".conf",
		".html", ".htm", ".xml", ".svg", ".css", ".scss", ".sass", ".less", ".js", ".jsx",
		".ts", ".tsx", ".json", ".yaml", ".yml", ".sql", ".py", ".pyw", ".pyi", ".ps1",
		".psm1", ".psd1", ".bat", ".cmd", ".sh", ".bash", ".zsh", ".md", ".markdown",
	} {
		if strings.HasSuffix(name, extension) {
			return true
		}
	}
	return false
}

func scanSearchText(relativePath, fileName, query, text string, pageIndex *int, occurrenceBase, limit int) []SearchHit {
	queryRunes := []rune(query)
	textRunes := []rune(text)
	if len(queryRunes) == 0 || limit <= 0 || len(queryRunes) > len(textRunes) {
		return []SearchHit{}
	}
	queryFolded := make([]rune, len(queryRunes))
	for i, r := range queryRunes {
		queryFolded[i] = foldRune(r)
	}
	pattern := kmpPrefix(queryFolded)
	offsets := make([]int, len(textRunes)+1)
	lineNumbers := make([]int, len(textRunes)+1)
	lineStarts := make([]int, len(textRunes)+1)
	line, lineStart := 1, 0
	for i, r := range textRunes {
		offsets[i+1] = offsets[i] + utf16RuneWidth(r)
		lineNumbers[i] = line
		lineStarts[i] = lineStart
		if r == '\n' || (r == '\r' && (i+1 == len(textRunes) || textRunes[i+1] != '\n')) {
			line++
			lineStart = offsets[i+1]
		}
	}
	lineNumbers[len(textRunes)] = line
	lineStarts[len(textRunes)] = lineStart
	queryUTF16 := utf16Length(query)
	hits := make([]SearchHit, 0, min(limit, 16))
	matched := 0
	for i, r := range textRunes {
		folded := foldRune(r)
		for matched > 0 && folded != queryFolded[matched] {
			matched = pattern[matched-1]
		}
		if folded == queryFolded[matched] {
			matched++
		}
		if matched != len(queryFolded) {
			continue
		}
		startRune := i + 1 - len(queryFolded)
		startUTF16 := offsets[startRune]
		snippet, snippetStart, snippetEnd := searchSnippet(textRunes, offsets, lineStarts, startRune, len(queryRunes), queryUTF16)
		hit := SearchHit{
			RelativePath: relativePath, FileName: fileName, Query: query, Snippet: snippet,
			SnippetMatchStart: snippetStart, SnippetMatchEnd: snippetEnd,
			Offset: startUTF16, OccurrenceIndex: occurrenceBase + len(hits),
		}
		if pageIndex != nil {
			page := *pageIndex
			withinPage := len(hits)
			hit.PageIndex = &page
			hit.PageOccurrenceIndex = &withinPage
		} else {
			lineNo := lineNumbers[startRune]
			column := startUTF16 - lineStarts[startRune] + 1
			hit.Line, hit.Column = &lineNo, &column
		}
		hits = append(hits, hit)
		if len(hits) >= limit {
			break
		}
		matched = 0 // Source returns non-overlapping occurrences.
	}
	return hits
}

func searchSnippet(text []rune, offsets, lineStarts []int, matchRune, queryRunes, queryUTF16 int) (string, int, int) {
	matchOffset := offsets[matchRune]
	lineStart := lineStarts[matchRune]
	targetStart := max(matchOffset-64, lineStart)
	startRune := lowerBound(offsets, targetStart)
	if startRune > matchRune {
		startRune = matchRune
	}
	minimumEnd := matchOffset + min(queryUTF16, 176)
	targetEnd := min(max(matchOffset+96, minimumEnd), min(offsets[len(offsets)-1], offsets[startRune]+240))
	matchEndRune := min(matchRune+queryRunes, len(text))
	for i := matchEndRune; i < len(text); i++ {
		if text[i] == '\n' {
			targetEnd = min(targetEnd, offsets[i])
			break
		}
	}
	endRune := lowerBound(offsets, targetEnd)
	if endRune > len(text) {
		endRune = len(text)
	}
	if endRune < matchRune+queryRunes {
		endRune = min(matchRune+queryRunes, len(text))
	}
	prefix := startRune > lowerBound(offsets, lineStart)
	suffix := endRune < len(text) && text[endRune] != '\n'
	var builder strings.Builder
	if prefix {
		builder.WriteRune('…')
	}
	for _, r := range text[startRune:endRune] {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		builder.WriteRune(r)
	}
	if suffix {
		builder.WriteRune('…')
	}
	matchStart := offsets[matchRune] - offsets[startRune]
	if prefix {
		matchStart++
	}
	matchEnd := min(matchStart+queryUTF16, utf16Length(builder.String()))
	return builder.String(), matchStart, matchEnd
}

func lowerBound(values []int, target int) int {
	return sort.Search(len(values), func(i int) bool { return values[i] >= target })
}

func kmpPrefix(pattern []rune) []int {
	prefix := make([]int, len(pattern))
	for i, matched := 1, 0; i < len(pattern); i++ {
		for matched > 0 && pattern[i] != pattern[matched] {
			matched = prefix[matched-1]
		}
		if pattern[i] == pattern[matched] {
			matched++
		}
		prefix[i] = matched
	}
	return prefix
}

func foldRune(value rune) rune {
	minimum := value
	for next := unicode.SimpleFold(value); next != value; next = unicode.SimpleFold(next) {
		if next < minimum {
			minimum = next
		}
	}
	return minimum
}

func utf16Length(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func utf16RuneWidth(value rune) int {
	if value > 0xFFFF {
		return 2
	}
	return 1
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
