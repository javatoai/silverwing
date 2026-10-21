package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"silverwing-go/internal/domain"
)

func (state *compareState) files(ctx context.Context) ([]CompareChange, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := state.service.runInState(ctx, state, maxCompareFilesOutput, true, nil,
		"diff", "--raw", "-z", "--no-abbrev", "--find-renames", "--no-ext-diff", "--no-textconv", state.baseTree, state.targetTree)
	if err != nil {
		return nil, fmt.Errorf("读取 Git 差异文件列表失败：%w", err)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return nil, errors.New("Git 差异文件列表超过安全上限")
	}
	files, err := parseRawCompareFiles([]byte(result.Stdout))
	if err != nil {
		return nil, err
	}
	return files, nil
}

func parseRawCompareFiles(output []byte) ([]CompareChange, error) {
	parts := bytes.Split(output, []byte{0})
	files := make([]CompareChange, 0)
	for i := 0; i < len(parts); {
		if len(parts[i]) == 0 {
			i++
			continue
		}
		header := string(parts[i])
		i++
		if !strings.HasPrefix(header, ":") {
			return nil, fmt.Errorf("Git 差异文件列表格式无效：%q", header)
		}
		fields := strings.Fields(strings.TrimPrefix(header, ":"))
		if len(fields) < 5 || i >= len(parts) {
			return nil, errors.New("Git 差异文件列表缺少文件状态或路径")
		}
		status := fields[4]
		oldPath := string(parts[i])
		i++
		change := CompareChange{Path: oldPath, baseBlob: fields[2], targetBlob: fields[3]}
		if status == "" {
			return nil, errors.New("Git 差异文件状态为空")
		}
		switch status[0] {
		case 'A':
			change.Kind = string(domain.WorkspaceGitAdded)
		case 'D':
			change.Kind = string(domain.WorkspaceGitDeleted)
		case 'M':
			change.Kind = string(domain.WorkspaceGitModified)
		case 'T':
			change.Kind = string(domain.WorkspaceGitTypeChanged)
		case 'R':
			if i >= len(parts) {
				return nil, errors.New("Git 重命名差异缺少新路径")
			}
			change.OldPath = oldPath
			change.Path = string(parts[i])
			change.Kind = string(domain.WorkspaceGitRenamed)
			i++
		case 'C':
			if i >= len(parts) {
				return nil, errors.New("Git 复制差异缺少新路径")
			}
			change.OldPath = oldPath
			change.Path = string(parts[i])
			change.Kind = string(domain.WorkspaceGitCopied)
			i++
		case 'U':
			change.Kind = string(domain.WorkspaceGitConflicted)
		default:
			return nil, fmt.Errorf("Git 差异状态暂不支持：%s", status)
		}
		if change.Path == "" {
			return nil, errors.New("Git 差异中包含空文件路径")
		}
		files = append(files, change)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func matchCompareChange(files []CompareChange, requested CompareChange) (CompareChange, bool) {
	for _, candidate := range files {
		if candidate.Path == requested.Path && candidate.OldPath == requested.OldPath && candidate.Kind == requested.Kind {
			return candidate, true
		}
	}
	return CompareChange{}, false
}

func (state *compareState) preview(ctx context.Context, change CompareChange) (WorkspaceGitFilePreview, error) {
	preview, _, err := state.previewWithCacheability(ctx, change)
	return preview, err
}

func (state *compareState) previewWithCacheability(ctx context.Context, change CompareChange) (WorkspaceGitFilePreview, bool, error) {
	return state.previewWithInputs(ctx, change, nil, true, nil)
}

// previewWithInputs shares the single-file comparison builder with bounded
// batches. A nil input requests the original, independently bounded Git read.
func (state *compareState) previewWithInputs(ctx context.Context, change CompareChange, preparedBlobs []compareBlobResult, blobCacheable bool, patch *string) (WorkspaceGitFilePreview, bool, error) {
	preview := WorkspaceGitFilePreview{Language: WorkspaceFileLanguageFromPath(change.Path)}
	kind := domain.WorkspaceGitFileChangeKind(change.Kind)
	oldPath := change.Path
	if change.OldPath != "" {
		oldPath = change.OldPath
	}
	requests, oldIndex, newIndex := state.previewBlobRequests(change)
	var oldContent, newContent *WorkspaceFilePreviewContent
	var oldReason, newReason string
	cacheable := true
	blobStarted := time.Now()
	blobResults := preparedBlobs
	var blobErr error
	if blobResults == nil {
		blobResults, blobCacheable, blobErr = state.readBlobs(ctx, requests)
	}
	observeGitComparePhase(ctx, "blob_read", blobStarted, map[string]any{"blobs": len(requests)})
	if blobErr != nil {
		return WorkspaceGitFilePreview{}, false, blobErr
	}
	cacheable = cacheable && blobCacheable
	if oldIndex >= 0 {
		oldContent, oldReason = blobResults[oldIndex].content, blobResults[oldIndex].reason
	}
	if newIndex >= 0 {
		newContent, newReason = blobResults[newIndex].content, blobResults[newIndex].reason
	}
	if err := ctx.Err(); err != nil {
		return WorkspaceGitFilePreview{}, false, err
	}
	preview.Content = newContent
	if preview.Content == nil {
		preview.Content = oldContent
	}
	preview.UnavailableReason = firstNonEmpty(newReason, oldReason)
	switch {
	case kind == domain.WorkspaceGitAdded:
		preview.Comparison = singleFileComparison(newContent, PreviewAdded)
	case kind == domain.WorkspaceGitDeleted:
		preview.Comparison = singleFileComparison(oldContent, PreviewDeleted)
	default:
		oldGitPath := oldPath
		newGitPath := change.Path
		diffStarted := time.Now()
		patchText := ""
		if patch != nil {
			patchText = *patch
		} else {
			diff, err := state.service.runInState(ctx, state, 3*MaxWorkspaceFilePreviewBytes, false, nil,
				"--literal-pathspecs", "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--unified=0", "--find-renames", state.baseTree, state.targetTree, "--", oldGitPath, newGitPath)
			if err != nil || diff.StdoutTruncated || diff.StderrTruncated {
				preview.Notice = "差异内容超过预览上限，无法生成完整左右对比。"
				cacheable = false
				observeGitComparePhase(ctx, "diff", diffStarted, map[string]any{"failed": true})
				break
			}
			if diff.ExitCode != 0 {
				preview.Notice = "无法读取 Git 差异，已显示可读取的文件内容。"
				cacheable = false
				observeGitComparePhase(ctx, "diff", diffStarted, map[string]any{"failed": true})
				break
			}
			patchText = diff.Stdout
		}
		observeGitComparePhase(ctx, "diff", diffStarted, map[string]any{"bytes": len(patchText)})
		if strings.Contains(patchText, "GIT binary patch") || strings.Contains(patchText, "Binary files ") {
			preview.UnavailableReason = "二进制文件暂不支持文本差异预览"
			preview.Notice = preview.UnavailableReason
			cacheable = false
			break
		}
		parseStarted := time.Now()
		hunks := parseDiffHunks(patchText)
		observeGitComparePhase(ctx, "parse", parseStarted, map[string]any{"hunks": len(hunks)})
		if kind == domain.WorkspaceGitRenamed && len(hunks) == 0 {
			var identical bool
			preview.Comparison, identical = renamedFileComparison(oldContent, newContent)
			if preview.Comparison != nil && !identical {
				preview.Notice = "重命名已检测到，但无法生成完整左右对比。"
			}
		} else {
			preview.Comparison = buildFileComparison(oldContent, newContent, hunks)
		}
		if preview.Comparison == nil {
			cacheable = false
			switch {
			case oldContent != nil && oldContent.Truncated || newContent != nil && newContent.Truncated:
				preview.Notice = "文件超过预览上限，无法生成完整左右对比。"
			case preview.UnavailableReason != "":
				preview.Notice = preview.UnavailableReason
			default:
				preview.Notice = "无法生成完整左右对比，已显示可读取的文件内容。"
			}
		}
	}
	if preview.Comparison == nil && preview.Notice == "" && preview.UnavailableReason != "" {
		preview.Notice = preview.UnavailableReason
	}
	if err := ctx.Err(); err != nil {
		return WorkspaceGitFilePreview{}, false, err
	}
	return preview, cacheable, nil
}

func (state *compareState) previewBlobRequests(change CompareChange) (requests []compareBlobRequest, oldIndex, newIndex int) {
	oldPath := firstNonEmpty(change.OldPath, change.Path)
	kind := domain.WorkspaceGitFileChangeKind(change.Kind)
	oldOrigin, newOrigin := PreviewOriginHead, PreviewOriginWorktree
	switch state.mode {
	case CompareMainline:
		oldOrigin = PreviewOriginMainline
		if !state.includeWorkingTree {
			newOrigin = PreviewOriginHead
		}
	case CompareCommits:
		if state.simulateOnMainline {
			oldOrigin, newOrigin = PreviewOriginMainline, PreviewOriginSimulated
		} else {
			oldOrigin, newOrigin = PreviewOriginBaseline, PreviewOriginSelectedCommits
		}
	}
	oldIndex, newIndex = -1, -1
	if kind != domain.WorkspaceGitAdded {
		oldIndex = len(requests)
		requests = append(requests, compareBlobRequest{objectID: change.baseBlob, path: oldPath, origin: oldOrigin})
	}
	if kind != domain.WorkspaceGitDeleted {
		newIndex = len(requests)
		requests = append(requests, compareBlobRequest{objectID: change.targetBlob, path: change.Path, origin: newOrigin})
	}
	return requests, oldIndex, newIndex
}

func renamedFileComparison(old, current *WorkspaceFilePreviewContent) (*WorkspaceFileComparison, bool) {
	if old == nil || current == nil || old.Truncated || current.Truncated {
		return nil, false
	}
	oldLines, newLines := previewLines(old.Text), previewLines(current.Text)
	comparison := &WorkspaceFileComparison{
		Rows:       make([]WorkspaceFileComparisonRow, 0, len(oldLines)),
		OldContent: old,
		NewContent: current,
	}
	if len(oldLines) != len(newLines) {
		return comparison, false
	}
	for index, oldLine := range oldLines {
		if oldLine != newLines[index] {
			return comparison, false
		}
		oldRow := WorkspaceFileComparisonLine{Kind: PreviewContext, LineNumber: index + 1, Text: oldLine}
		newRow := WorkspaceFileComparisonLine{Kind: PreviewContext, LineNumber: index + 1, Text: newLines[index]}
		comparison.Rows = append(comparison.Rows, WorkspaceFileComparisonRow{OldLine: &oldRow, NewLine: &newRow})
	}
	return comparison, true
}

func (state *compareState) readBlob(ctx context.Context, tree, path string, origin WorkspaceFileContentOrigin) (*WorkspaceFilePreviewContent, string) {
	if err := ctx.Err(); err != nil {
		return nil, err.Error()
	}
	if _, err := normalizePreviewPath(state.root, path); err != nil {
		return nil, err.Error()
	}
	reference := tree + ":" + filepath.ToSlash(path)
	sizeResult, err := state.service.runInState(ctx, state, 1024, false, nil, "cat-file", "-s", reference)
	if err != nil || sizeResult.ExitCode != 0 {
		return nil, "Git 基线中不存在该文件，无法预览"
	}
	size, err := strconv.ParseInt(strings.TrimSpace(sizeResult.Stdout), 10, 64)
	if err != nil || size < 0 {
		return nil, "无法读取 Git 文件大小"
	}
	if size > MaxWorkspaceFilePreviewBytes {
		return nil, "文件超过 512 KiB 预览上限，暂不支持预览"
	}
	content, err := state.service.runInState(ctx, state, MaxWorkspaceFilePreviewBytes+1, false, nil, "cat-file", "blob", reference)
	if err != nil || content.ExitCode != 0 {
		return nil, "无法读取 Git 文件内容"
	}
	decoded, err := decodePreview([]byte(content.Stdout), origin)
	if err != nil {
		return nil, err.Error()
	}
	if decoded.content == nil {
		return nil, decoded.reason
	}
	if bytes.IndexByte([]byte(decoded.content.Text), 0) >= 0 || !utf8.ValidString(decoded.content.Text) {
		return nil, "疑似二进制文件，暂不支持文本预览"
	}
	return decoded.content, ""
}

type compareBlobRequest struct {
	objectID string
	path     string
	origin   WorkspaceFileContentOrigin
}

type compareBlobResult struct {
	content *WorkspaceFilePreviewContent
	reason  string
}

type compareBlobMetadata struct {
	request compareBlobRequest
	object  string
	size    int64
	result  compareBlobResult
	read    bool
}

// readBlobs validates the object sizes first, then obtains all eligible blobs
// in one cat-file process. Paths are validated for workspace containment, but
// object IDs from the raw diff drive the read so special filenames are safe.
func (state *compareState) readBlobs(ctx context.Context, requests []compareBlobRequest) ([]compareBlobResult, bool, error) {
	// The same blob can occur in several paths and on both sides. Validate every
	// path, but ask Git for each immutable object only once.
	unique := make([]compareBlobRequest, 0, len(requests))
	byID := make(map[string]int, len(requests))
	indexes := make([]int, len(requests))
	for i, request := range requests {
		if _, err := normalizePreviewPath(state.root, request.path); err != nil {
			return nil, false, err
		}
		index, exists := byID[request.objectID]
		if !exists {
			index = len(unique)
			byID[request.objectID] = index
			unique = append(unique, request)
		}
		indexes[i] = index
	}
	read, cacheable, err := state.readDistinctBlobs(ctx, unique)
	if err != nil {
		return nil, false, err
	}
	results := make([]compareBlobResult, len(requests))
	for i, index := range indexes {
		results[i] = read[index]
		if read[index].content != nil {
			content := *read[index].content
			content.Origin = requests[i].origin
			results[i].content = &content
		}
	}
	return results, cacheable, nil
}

func (state *compareState) readDistinctBlobs(ctx context.Context, requests []compareBlobRequest) ([]compareBlobResult, bool, error) {
	results := make([]compareBlobResult, len(requests))
	if len(requests) == 0 {
		return results, true, nil
	}
	metadata := make([]compareBlobMetadata, len(requests))
	ids := make([]string, 0, len(requests))
	indexes := make([]int, 0, len(requests))
	cacheable := true
	for index, request := range requests {
		metadata[index].request = request
		if _, err := normalizePreviewPath(state.root, request.path); err != nil {
			metadata[index].result.reason = err.Error()
			cacheable = false
			continue
		}
		if !validGitObjectID(request.objectID) {
			metadata[index].result.reason = "Git 快照中缺少文件对象，无法预览"
			cacheable = false
			continue
		}
		ids = append(ids, request.objectID)
		indexes = append(indexes, index)
	}
	if len(ids) == 0 {
		return results, false, nil
	}
	input := []byte(strings.Join(ids, "\n") + "\n")
	check, err := state.service.runInState(ctx, state, max(16*1024, len(ids)*256), false, input,
		"cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)")
	if err != nil {
		return nil, false, err
	}
	if check.ExitCode != 0 || check.StdoutTruncated || check.StderrTruncated {
		for _, index := range indexes {
			metadata[index].result.reason = "无法读取 Git 文件元数据"
		}
		return collectCompareBlobResults(metadata), false, nil
	}
	lines := strings.Split(strings.TrimSuffix(check.Stdout, "\n"), "\n")
	if len(lines) != len(indexes) {
		for _, index := range indexes {
			metadata[index].result.reason = "Git 返回的文件元数据不完整"
		}
		return collectCompareBlobResults(metadata), false, nil
	}
	readIDs := make([]string, 0, len(indexes))
	readIndexes := make([]int, 0, len(indexes))
	for lineIndex, line := range lines {
		index := indexes[lineIndex]
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "missing" {
			metadata[index].result.reason = "Git 基线中不存在该文件，无法预览"
			cacheable = false
			continue
		}
		if len(fields) != 3 || !strings.EqualFold(fields[0], metadata[index].request.objectID) {
			metadata[index].result.reason = "Git 返回的文件元数据格式无效"
			cacheable = false
			continue
		}
		metadata[index].object = fields[0]
		if fields[1] != "blob" {
			metadata[index].result.reason = "该 Git 对象不是可预览的文本文件"
			cacheable = false
			continue
		}
		size, parseErr := strconv.ParseInt(fields[2], 10, 64)
		if parseErr != nil || size < 0 {
			metadata[index].result.reason = "无法读取 Git 文件大小"
			cacheable = false
			continue
		}
		metadata[index].size = size
		if size > MaxWorkspaceFilePreviewBytes {
			metadata[index].result.reason = "文件超过 512 KiB 预览上限，暂不支持预览"
			cacheable = false
			continue
		}
		metadata[index].read = true
		readIDs = append(readIDs, metadata[index].object)
		readIndexes = append(readIndexes, index)
	}
	if len(readIDs) == 0 {
		return collectCompareBlobResults(metadata), false, nil
	}
	readInput := []byte(strings.Join(readIDs, "\n") + "\n")
	limit := len(readIDs)*(MaxWorkspaceFilePreviewBytes+256) + 4096
	batch, err := state.service.runInState(ctx, state, limit, false, readInput, "cat-file", "--batch")
	if err != nil {
		return nil, false, err
	}
	if batch.ExitCode != 0 || batch.StdoutTruncated || batch.StderrTruncated {
		for _, index := range readIndexes {
			metadata[index].result.reason = "无法读取 Git 文件内容"
		}
		return collectCompareBlobResults(metadata), false, nil
	}
	data := []byte(batch.Stdout)
	position := 0
	for _, index := range readIndexes {
		lineEnd := bytes.IndexByte(data[position:], '\n')
		if lineEnd < 0 {
			metadata[index].result.reason = "Git 返回的文件内容不完整"
			cacheable = false
			break
		}
		header := strings.Fields(string(data[position : position+lineEnd]))
		position += lineEnd + 1
		if len(header) != 3 || !strings.EqualFold(header[0], metadata[index].object) || header[1] != "blob" {
			metadata[index].result.reason = "Git 返回的文件内容格式无效"
			cacheable = false
			break
		}
		size, parseErr := strconv.ParseInt(header[2], 10, 64)
		if parseErr != nil || size != metadata[index].size || size < 0 || int64(len(data)-position) < size+1 {
			metadata[index].result.reason = "Git 返回的文件内容长度无效"
			cacheable = false
			break
		}
		contentBytes := data[position : position+int(size)]
		position += int(size)
		if data[position] != '\n' {
			metadata[index].result.reason = "Git 返回的文件内容分隔符无效"
			cacheable = false
			break
		}
		position++
		decoded, decodeErr := decodePreview(contentBytes, metadata[index].request.origin)
		if decodeErr != nil {
			metadata[index].result.reason = decodeErr.Error()
			cacheable = false
			continue
		}
		if decoded.content == nil {
			metadata[index].result.reason = decoded.reason
			cacheable = false
			continue
		}
		if bytes.IndexByte([]byte(decoded.content.Text), 0) >= 0 || !utf8.ValidString(decoded.content.Text) {
			metadata[index].result.reason = "疑似二进制文件，暂不支持文本预览"
			cacheable = false
			continue
		}
		metadata[index].result.content = decoded.content
	}
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	return collectCompareBlobResults(metadata), cacheable, nil
}

func collectCompareBlobResults(metadata []compareBlobMetadata) []compareBlobResult {
	results := make([]compareBlobResult, len(metadata))
	for index := range metadata {
		results[index] = metadata[index].result
	}
	return results
}

func validGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, digit := range value {
		isDigit := digit >= '0' && digit <= '9'
		isLowerHex := digit >= 'a' && digit <= 'f'
		isUpperHex := digit >= 'A' && digit <= 'F'
		if !isDigit && !isLowerHex && !isUpperHex {
			return false
		}
	}
	return strings.Trim(value, "0") != ""
}
