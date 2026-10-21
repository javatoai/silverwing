package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type feishuGateway struct {
	ctx     context.Context
	service *Service
	config  domain.AppConfig
}

func newFeishuGateway(ctx context.Context, service *Service, config domain.AppConfig) *feishuGateway {
	return &feishuGateway{ctx: ctx, service: service, config: config}
}

func (g *feishuGateway) invoke(args []string, input []byte, directory, operation string, timeout time.Duration) (map[string]any, error) {
	return g.invokeWithPolicy(args, input, directory, operation, timeout, feishuInvocationPolicy{})
}

func (g *feishuGateway) invokeWithPolicy(args []string, input []byte, directory, operation string, timeout time.Duration, policy feishuInvocationPolicy) (map[string]any, error) {
	for retry := 0; ; retry++ {
		if err := g.ctx.Err(); err != nil {
			return nil, err
		}
		if retry > 0 && policy.beforeRetry != nil {
			if err := policy.beforeRetry(); err != nil {
				return nil, err
			}
		}
		if policy.beforeAttempt != nil {
			if err := policy.beforeAttempt(); err != nil {
				return nil, err
			}
		}
		if err := g.ctx.Err(); err != nil {
			return nil, err
		}
		result, runErr := g.service.runLark(g.ctx, g.config, args, input, timeout, operation, directory)
		if process.IsCleanupFailure(runErr) {
			return nil, runErr
		}
		if err := g.ctx.Err(); err != nil {
			return nil, err
		}
		if errors.Is(runErr, context.Canceled) {
			return nil, runErr
		}
		output := result.Stdout
		if !result.Successful() && strings.TrimSpace(result.Stderr) != "" {
			output = result.Stderr
		}
		root := decodeCLIEnvelope(output)
		failure := larkResponseFailure(root, runErr, result.ExitCode)
		if errors.Is(runErr, context.DeadlineExceeded) {
			failure = &larkResponseError{message: runErr.Error(), timedOut: true, cause: runErr}
		}
		if failure != nil {
			failure.message = fmt.Sprintf("%s：%s", operation, g.service.safeDiagnostic(g.config, failure.message))
			recovery := policy.recoverWrite != nil && failure.retryableUploadFailure()
			safeRetry := !failure.timedOut && retryableLarkFailure(failure, args) &&
				(failure.code == nil || *failure.code != policy.noRetryCode || policy.noRetryCode == 0)
			maxRetries := 8
			if recovery && !safeRetry {
				maxRetries = 2
			}
			if (safeRetry || recovery) && retry < maxRetries {
				wait := waitLarkRetry
				if !safeRetry {
					wait = waitLarkUploadRecovery
				}
				if err := wait(g.ctx, retry+1, failure.retryAfter); err != nil {
					return nil, err
				}
				if recovery {
					recovered, err := policy.recoverWrite(failure)
					if process.IsCleanupFailure(err) {
						return nil, err
					}
					if ctxErr := g.ctx.Err(); ctxErr != nil {
						return nil, ctxErr
					}
					if err != nil {
						return nil, err
					}
					if recovered != nil {
						return recovered, nil
					}
				}
				continue
			}
			if runErr != nil && failure.code == nil && failure.status == nil && !failure.timedOut {
				var commandFailure *commandError
				if errors.As(runErr, &commandFailure) {
					return nil, failure
				}
				return nil, runErr
			}
			return nil, failure
		}
		if runErr != nil {
			return nil, runErr
		}
		if root == nil {
			return nil, &larkResponseError{message: fmt.Sprintf("%s 响应不是有效 JSON 对象", operation)}
		}
		return root, nil
	}
}

type larkResponseError struct {
	message    string
	subtype    string
	code       *int64
	status     *int64
	retryAfter time.Duration
	retryable  bool
	timedOut   bool
	cause      error
}

func (e *larkResponseError) Error() string { return e.message }
func (e *larkResponseError) Unwrap() error {
	// The owner context is checked separately. A child command timeout is a
	// failed/ambiguous Feishu operation, not cancellation of the sync session.
	if e.timedOut {
		return nil
	}
	return e.cause
}

func decodeCLIEnvelope(raw string) map[string]any {
	if value, err := decodeJSON(raw); err == nil {
		if root := object(value); root != nil {
			return root
		}
	}
	trimmed := strings.TrimSpace(raw)
	start, end := strings.IndexByte(trimmed, '{'), strings.LastIndexByte(trimmed, '}')
	if start >= 0 && end > start {
		if value, err := decodeJSON(trimmed[start : end+1]); err == nil {
			return object(value)
		}
	}
	return nil
}

func larkResponseFailure(root map[string]any, runErr error, exitCode int) *larkResponseError {
	if root == nil {
		if runErr == nil && exitCode == 0 {
			return nil
		}
		message := "lark-cli 未返回有效 JSON"
		if runErr != nil {
			message = runErr.Error()
		}
		return &larkResponseError{message: message, cause: runErr}
	}
	problem := object(root["error"])
	code, hasCode := integer(problem["code"])
	if !hasCode {
		code, hasCode = integer(root["code"])
	}
	status, hasStatus := integer(problem["http_status"])
	if !hasStatus {
		status, hasStatus = integer(problem["status"])
	}
	if !hasStatus {
		status, hasStatus = integer(root["http_status"])
	}
	ok, hasOK := boolean(root["ok"])
	failed := (hasCode && code != 0) || (hasStatus && status >= 400) || (hasOK && !ok) || exitCode != 0
	if !failed {
		return nil
	}
	message := firstFieldText(problem, "message", "msg")
	if message == "" {
		message = firstFieldText(root, "msg", "message")
	}
	if message == "" && runErr != nil {
		message = runErr.Error()
	}
	if message == "" {
		message = "飞书 CLI 返回错误"
	}
	retryable, _ := boolean(problem["retryable"])
	result := &larkResponseError{message: message, subtype: text(problem["subtype"]), retryable: retryable, cause: runErr}
	if hasCode {
		result.code = &code
	}
	if hasStatus {
		result.status = &status
	}
	result.retryAfter = larkRetryAfter(problem["retry_after_seconds"])
	return result
}

func larkRetryAfter(value any) time.Duration {
	seconds, err := strconv.ParseFloat(text(value), 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return 0
	}
	if seconds > 300 {
		seconds = 300
	}
	return time.Duration(seconds * float64(time.Second))
}

func retryableLarkFailure(failure *larkResponseError, args []string) bool {
	if failure.subtype == "rate_limit" || (failure.status != nil && *failure.status == 429) || (failure.code != nil && *failure.code == 99991400) {
		return true
	}
	return len(args) >= 3 && args[0] == "wiki" && args[1] == "nodes" && args[2] == "create" && failure.code != nil && *failure.code == 131009
}

func waitLarkRetry(ctx context.Context, retryNumber int, retryAfter time.Duration) error {
	delay := retryAfter
	if delay <= 0 {
		seconds := []int{1, 2, 4, 8, 15, 15, 15, 15}[retryNumber-1]
		delay = time.Duration(seconds)*time.Second + time.Duration(rand.Int63n(2001))*time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (g *feishuGateway) ResolveTarget(raw string) (domain.FeishuWikiNode, error) {
	validated, err := domain.ValidateMaterialsFeishuTarget(raw)
	if err != nil || validated == nil {
		return domain.FeishuWikiNode{}, errors.New("请先设置飞书知识库地址")
	}
	u, _ := url.Parse(*validated)
	nodeToken := strings.Trim(strings.TrimPrefix(u.Path, "/wiki/"), "/")
	node, err := g.GetNode(nodeToken)
	if err != nil {
		return domain.FeishuWikiNode{}, err
	}
	if node == nil {
		return domain.FeishuWikiNode{}, errors.New("飞书知识库节点不存在")
	}
	return *node, nil
}

func (g *feishuGateway) GetNode(token string) (*domain.FeishuWikiNode, error) {
	if token == "" {
		return nil, errors.New("飞书节点 token 不能为空")
	}
	root, err := g.invoke([]string{"wiki", "+node-get", "--node-token", token, "--as", "user", "--format", "json"}, nil, "", "读取知识库节点", 25*time.Second)
	if err != nil {
		if process.IsCleanupFailure(err) {
			return nil, err
		}
		var failure *larkResponseError
		if errors.As(err, &failure) && (failure.subtype == "not_found" ||
			failure.code != nil && (*failure.code == 131005 || *failure.code == 404) || failure.status != nil && *failure.status == 404) {
			return nil, nil
		}
		return nil, err
	}
	node, err := parseFeishuNode(root)
	if err != nil {
		return nil, err
	}
	return &node, nil
}

func (g *feishuGateway) ListChildren(parent domain.FeishuWikiNode) ([]domain.FeishuWikiNode, error) {
	children := []domain.FeishuWikiNode{}
	seenNodes, seenCursors := map[string]bool{}, map[string]bool{}
	cursor := ""
	for page := 0; page < 1000; page++ {
		args := []string{"wiki", "+node-list", "--space-id", parent.SpaceID, "--parent-node-token", parent.NodeToken, "--page-size", "50", "--as", "user", "--format", "json"}
		if cursor != "" {
			args = append(args, "--page-token", cursor)
		}
		root, err := g.invoke(args, nil, "", "读取知识库子节点", 30*time.Second)
		if err != nil {
			return nil, err
		}
		data := responsePayload(root)
		rows, ok := data["nodes"].([]any)
		if !ok {
			return nil, errors.New("读取知识库子节点成功响应缺少 nodes 列表")
		}
		for _, row := range rows {
			node, err := parseFeishuNodeObject(object(row), root)
			if err != nil {
				return nil, err
			}
			if node.SpaceID != parent.SpaceID || node.ParentNodeToken != parent.NodeToken {
				return nil, errors.New("知识库子节点的空间或父节点与请求不一致")
			}
			if seenNodes[node.NodeToken] {
				return nil, errors.New("知识库分页返回重复节点，无法确认目录清单完整")
			}
			seenNodes[node.NodeToken] = true
			children = append(children, node)
		}
		hasMore, ok := boolean(data["has_more"])
		if !ok {
			return nil, errors.New("读取知识库子节点成功响应缺少 has_more")
		}
		if !hasMore {
			return children, nil
		}
		cursor = text(data["page_token"])
		if cursor == "" || seenCursors[cursor] {
			return nil, errors.New("知识库仍有下一页，但分页游标缺失或重复")
		}
		seenCursors[cursor] = true
	}
	return nil, errors.New("知识库分页超过读取上限")
}

func (g *feishuGateway) DocumentMetadata(node domain.FeishuWikiNode) (domain.FeishuDocumentMetadata, error) {
	if !strings.EqualFold(node.ObjectType, "docx") {
		return domain.FeishuDocumentMetadata{}, errors.New("只有飞书在线文档支持读取文档元信息")
	}
	root, err := g.invoke([]string{"api", "GET", "/open-apis/docx/v1/documents/" + node.ObjectToken, "--as", "user", "--format", "json"}, nil, "", "读取飞书在线文档元信息", 25*time.Second)
	if err != nil {
		return domain.FeishuDocumentMetadata{}, err
	}
	doc := object(responsePayload(root)["document"])
	revision, ok := integer(doc["revision_id"])
	if !ok || revision < 0 || text(doc["title"]) == "" {
		return domain.FeishuDocumentMetadata{}, errors.New("飞书在线文档元信息缺少 revision_id 或 title")
	}
	return domain.FeishuDocumentMetadata{Revision: revision, Title: text(doc["title"])}, nil
}

func (g *feishuGateway) ReadDocument(node domain.FeishuWikiNode, includeComments bool) (domain.FeishuTextDocument, error) {
	if !strings.EqualFold(node.ObjectType, "docx") {
		return domain.FeishuTextDocument{}, errors.New("只有飞书在线文档可以读取 Markdown 正文")
	}
	root, err := g.invoke([]string{"docs", "+fetch", "--doc", node.ObjectToken, "--detail", "full", "--scope", "full", "--doc-format", "markdown", "--as", "user"}, nil, "", "读取飞书在线文档", 45*time.Second)
	if err != nil {
		return domain.FeishuTextDocument{}, err
	}
	doc := object(responsePayload(root)["document"])
	content, ok := doc["content"].(string)
	if !ok {
		return domain.FeishuTextDocument{}, errors.New("读取飞书在线文档成功响应缺少 Markdown 正文")
	}
	revision, ok := integer(doc["revision_id"])
	if !ok {
		metadata, err := g.DocumentMetadata(node)
		if err != nil {
			return domain.FeishuTextDocument{}, err
		}
		revision = metadata.Revision
	}
	content = stripMarkdownDocumentTitle(content, node.Title)
	document := domain.FeishuTextDocument{Content: content, Revision: revision}
	if includeComments {
		fingerprint, err := g.CommentsFingerprint(node)
		if err != nil {
			return domain.FeishuTextDocument{}, err
		}
		document.CommentsFingerprint = fingerprint
	}
	return document, nil
}

func (g *feishuGateway) Version(node domain.FeishuWikiNode, includeComments bool) (domain.FeishuRemoteVersion, error) {
	version := ""
	if strings.EqualFold(node.ObjectType, "docx") {
		metadata, err := g.DocumentMetadata(node)
		if err != nil {
			return domain.FeishuRemoteVersion{}, err
		}
		version = fmt.Sprint(metadata.Revision)
	} else if strings.EqualFold(node.ObjectType, "file") {
		root, err := g.invoke([]string{"drive", "+version-history", "--file-token", node.ObjectToken, "--limit", "1", "--as", "user", "--format", "json"}, nil, "", "读取飞书文件版本", 25*time.Second)
		if err != nil {
			return domain.FeishuRemoteVersion{}, err
		}
		versions := array(responsePayload(root)["versions"])
		if len(versions) == 0 || text(object(versions[0])["version"]) == "" {
			return domain.FeishuRemoteVersion{}, errors.New("飞书文件没有可用的版本记录")
		}
		version = text(object(versions[0])["version"])
	} else {
		return domain.FeishuRemoteVersion{}, fmt.Errorf("不支持读取该类型的飞书版本：%s", node.ObjectType)
	}
	result := domain.FeishuRemoteVersion{Version: version}
	if includeComments {
		fingerprint, err := g.CommentsFingerprint(node)
		if err != nil {
			return domain.FeishuRemoteVersion{}, err
		}
		result.CommentsFingerprint = fingerprint
	}
	return result, nil
}

func (g *feishuGateway) ReadFileFingerprint(node domain.FeishuWikiNode) (domain.FeishuFileFingerprint, error) {
	if !strings.EqualFold(node.ObjectType, "file") {
		return domain.FeishuFileFingerprint{}, errors.New("仅普通飞书文件支持上传内容核对")
	}
	current, err := g.GetNode(node.NodeToken)
	if process.IsCleanupFailure(err) {
		return domain.FeishuFileFingerprint{}, err
	}
	if err != nil || current == nil || *current != node {
		return domain.FeishuFileFingerprint{}, errors.New("核对上传前飞书文件身份或位置已改变")
	}
	before, err := g.Version(node, true)
	if err != nil {
		return domain.FeishuFileFingerprint{}, err
	}
	if err := os.MkdirAll(g.service.paths.Temp, 0o700); err != nil {
		return domain.FeishuFileFingerprint{}, err
	}
	directory, err := os.MkdirTemp(g.service.paths.Temp, "materials-file-proof-")
	if err != nil {
		return domain.FeishuFileFingerprint{}, err
	}
	defer os.RemoveAll(directory)
	output := filepath.Join(directory, "content.bin")
	if _, err := g.invoke([]string{"drive", "+download", "--file-token", node.ObjectToken, "--output", output, "--as", "user", "--format", "json"}, nil, "", "核对未确认的飞书文件上传", 10*time.Minute); err != nil {
		return domain.FeishuFileFingerprint{}, err
	}
	info, err := os.Lstat(output)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return domain.FeishuFileFingerprint{}, errors.New("飞书文件下载未返回普通文件")
	}
	hash, err := hashLocalFile(output)
	if err != nil {
		return domain.FeishuFileFingerprint{}, err
	}
	current, err = g.GetNode(node.NodeToken)
	if process.IsCleanupFailure(err) {
		return domain.FeishuFileFingerprint{}, err
	}
	if err != nil || current == nil || *current != node {
		return domain.FeishuFileFingerprint{}, errors.New("核对上传期间飞书文件身份或位置已改变")
	}
	after, err := g.Version(node, true)
	if err != nil {
		return domain.FeishuFileFingerprint{}, err
	}
	if after != before {
		return domain.FeishuFileFingerprint{}, errors.New("核对上传期间飞书文件版本或评论已改变")
	}
	return domain.FeishuFileFingerprint{SHA256: hash, RemoteVersion: before}, nil
}

func (g *feishuGateway) CommentsFingerprint(node domain.FeishuWikiNode) (string, error) {
	comments, err := g.listCommentCards(node)
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(comments)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func (g *feishuGateway) listCommentCards(node domain.FeishuWikiNode) ([]any, error) {
	items := []any{}
	cursor, seenCursors, seenIDs := "", map[string]bool{}, map[string]bool{}
	for page := 0; page < 1000; page++ {
		args := []string{"drive", "+list-comments", "--token", node.ObjectToken, "--type", node.ObjectType, "--solved-status", "all", "--page-size", "100", "--as", "user", "--format", "json"}
		if cursor != "" {
			args = append(args, "--page-token", cursor)
		}
		root, err := g.invoke(args, nil, "", "读取飞书文档评论", 25*time.Second)
		if err != nil {
			return nil, err
		}
		data := responsePayload(root)
		pageItems, ok := data["items"].([]any)
		if !ok {
			return nil, errors.New("读取飞书文档评论成功响应缺少 items 列表")
		}
		for _, item := range pageItems {
			comment := object(item)
			id := text(comment["comment_id"])
			if id == "" || seenIDs[id] {
				return nil, errors.New("飞书评论列表缺少唯一 comment_id")
			}
			seenIDs[id] = true
			replies, err := g.listCommentReplies(node, id)
			if err != nil {
				return nil, err
			}
			card := map[string]any{}
			for key, value := range comment {
				if key != "reply_list" && key != "has_more" && key != "page_token" {
					card[key] = value
				}
			}
			items = append(items, map[string]any{"comment": card, "replies": replies})
		}
		hasMore, ok := boolean(data["has_more"])
		if !ok {
			return nil, errors.New("读取飞书文档评论成功响应缺少 has_more")
		}
		if !hasMore {
			sort.Slice(items, func(i, j int) bool {
				return text(object(object(items[i])["comment"])["comment_id"]) < text(object(object(items[j])["comment"])["comment_id"])
			})
			return items, nil
		}
		cursor = text(data["page_token"])
		if cursor == "" || seenCursors[cursor] {
			return nil, errors.New("飞书评论分页游标缺失或重复")
		}
		seenCursors[cursor] = true
	}
	return nil, errors.New("飞书评论分页超过读取上限")
}

func (g *feishuGateway) listCommentReplies(node domain.FeishuWikiNode, commentID string) (map[string]any, error) {
	replies := map[string]any{}
	cursor, seen := "", map[string]bool{}
	for page := 0; page < 1000; page++ {
		args := []string{"drive", "+list-replies", "--token", node.ObjectToken, "--type", node.ObjectType, "--comment-id", commentID, "--page-size", "100", "--as", "user", "--format", "json"}
		if cursor != "" {
			args = append(args, "--page-token", cursor)
		}
		root, err := g.invoke(args, nil, "", "读取飞书评论回复", 25*time.Second)
		if err != nil {
			return nil, err
		}
		data := responsePayload(root)
		items, ok := data["items"].([]any)
		if !ok {
			return nil, errors.New("读取飞书评论回复成功响应缺少 items 列表")
		}
		for _, item := range items {
			row := object(item)
			id := text(row["reply_id"])
			if id == "" || replies[id] != nil {
				return nil, errors.New("飞书评论回复缺少唯一 reply_id")
			}
			if object(row["content"]) == nil || row["content"] == nil {
				return nil, errors.New("飞书评论回复缺少 content")
			}
			replies[id] = row
		}
		hasMore, ok := boolean(data["has_more"])
		if !ok {
			return nil, errors.New("读取飞书评论回复成功响应缺少 has_more")
		}
		if !hasMore {
			return replies, nil
		}
		cursor = text(data["page_token"])
		if cursor == "" || seen[cursor] {
			return nil, errors.New("飞书评论回复分页游标缺失或重复")
		}
		seen[cursor] = true
	}
	return nil, errors.New("飞书评论回复分页超过读取上限")
}

func (g *feishuGateway) CreateDirectory(parent domain.FeishuWikiNode, title string) (domain.FeishuWriteResult, error) {
	data, _ := json.Marshal(map[string]string{"node_type": "origin", "obj_type": "docx", "parent_node_token": parent.NodeToken, "title": title})
	root, err := g.invokeWithPolicy([]string{"wiki", "nodes", "create", "--space-id", parent.SpaceID, "--data", "-", "--as", "user", "--format", "json"}, data, "", "创建飞书目录节点", 60*time.Second,
		feishuInvocationPolicy{beforeRetry: func() error { return g.verifyMaterialsNode(parent) }})
	if err != nil {
		if process.IsCleanupFailure(err) {
			return domain.FeishuWriteResult{}, err
		}
		var failure *larkResponseError
		if errors.As(err, &failure) && failure.ambiguousCreateFailure() && materialsCreationOperationTitle.MatchString(title) {
			return g.recoverAmbiguousMaterialsCreate(parent, title, "目录", err)
		}
		return domain.FeishuWriteResult{}, err
	}
	node, err := parseFeishuNode(root)
	if err != nil {
		return domain.FeishuWriteResult{}, err
	}
	if node.ParentNodeToken != parent.NodeToken || node.SpaceID != parent.SpaceID || node.ObjectType != "docx" || node.Title != title {
		return domain.FeishuWriteResult{}, errors.New("飞书目录创建结果与请求不一致")
	}
	return domain.FeishuWriteResult{Node: node}, nil
}

func (g *feishuGateway) CreateDocument(parent domain.FeishuWikiNode, title, markdown, workingDirectory string, includeComments bool) (domain.FeishuWriteResult, error) {
	content := markdownWithTitle(title, markdown)
	args := []string{"docs", "+create", "--title", title, "--doc-format", "markdown", "--content", "-", "--parent-token", parent.NodeToken, "--as", "user"}
	root, err := g.invokeWithPolicy(args, []byte(content), workingDirectory, "创建飞书在线文档", 2*time.Minute,
		feishuInvocationPolicy{noRetryCode: 10020, beforeRetry: func() error { return g.verifyMaterialsNode(parent) }})
	if err != nil {
		if process.IsCleanupFailure(err) {
			return domain.FeishuWriteResult{}, err
		}
		var failure *larkResponseError
		if errors.As(err, &failure) && (failure.timedOut || failure.code != nil && *failure.code == 10020) && materialsCreationOperationTitle.MatchString(title) {
			return g.recoverAmbiguousMaterialsCreate(parent, title, "在线文档", err)
		}
		return domain.FeishuWriteResult{}, err
	}
	doc := object(responsePayload(root)["document"])
	token := text(doc["document_id"])
	if token == "" {
		return domain.FeishuWriteResult{}, errors.New("创建飞书在线文档成功响应缺少 document_id")
	}
	node, err := g.GetNode(token)
	if err != nil || node == nil {
		if err == nil {
			err = errors.New("创建后无法读取知识库节点")
		}
		return domain.FeishuWriteResult{}, err
	}
	if node.ParentNodeToken != parent.NodeToken || node.SpaceID != parent.SpaceID || node.ObjectType != "docx" || node.Title != title {
		return domain.FeishuWriteResult{}, errors.New("创建后的在线文档不属于指定父节点")
	}
	result := domain.FeishuWriteResult{Node: *node}
	if includeComments {
		version, err := g.Version(*node, true)
		if err != nil {
			return domain.FeishuWriteResult{}, err
		}
		result.Version = &version.Version
		result.CommentsFingerprint = &version.CommentsFingerprint
	}
	return result, nil
}

func (g *feishuGateway) OverwriteDocument(node domain.FeishuWikiNode, markdown string, expectedRevision int64, workingDirectory string, expectedCommentsFingerprint *string, expectedParent *domain.FeishuWikiNode) (domain.FeishuWriteResult, error) {
	current, err := g.GetNode(node.NodeToken)
	if process.IsCleanupFailure(err) {
		return domain.FeishuWriteResult{}, err
	}
	if err != nil || current == nil || *current != node {
		return domain.FeishuWriteResult{}, errors.New("飞书文档身份、位置或标题已改变，已取消覆盖")
	}
	metadata, err := g.DocumentMetadata(node)
	if err != nil {
		return domain.FeishuWriteResult{}, err
	}
	if metadata.Revision != expectedRevision || metadata.Title != node.Title {
		return domain.FeishuWriteResult{}, errors.New("飞书文档版本或标题已变化，已取消覆盖")
	}
	if expectedParent != nil && node.ParentNodeToken != expectedParent.NodeToken {
		return domain.FeishuWriteResult{}, errors.New("飞书文档父节点已变化，已取消覆盖")
	}
	if expectedCommentsFingerprint != nil {
		fingerprint, err := g.CommentsFingerprint(node)
		if process.IsCleanupFailure(err) {
			return domain.FeishuWriteResult{}, err
		}
		if err != nil || fingerprint != *expectedCommentsFingerprint {
			return domain.FeishuWriteResult{}, errors.New("飞书文档评论已变化，已取消覆盖")
		}
	}
	args := []string{"docs", "+update", "--doc", node.ObjectToken, "--command", "overwrite", "--doc-format", "markdown", "--content", "-", "--revision-id", fmt.Sprint(expectedRevision), "--as", "user"}
	root, err := g.invoke(args, []byte(markdownWithTitle(node.Title, markdown)), workingDirectory, "覆盖飞书在线文档", 2*time.Minute)
	if err != nil {
		return domain.FeishuWriteResult{}, err
	}
	updated := *current
	if doc := object(responsePayload(root)["document"]); text(doc["revision_id"]) != "" {
		// The readback below remains authoritative if the CLI omitted or normalized the receipt.
		_ = doc
	}
	verified, err := g.ReadDocument(updated, expectedCommentsFingerprint != nil)
	if process.IsCleanupFailure(err) {
		return domain.FeishuWriteResult{}, err
	}
	if err != nil || verified.Revision <= expectedRevision || verified.Content != strings.TrimSpace(markdown) {
		return domain.FeishuWriteResult{}, errors.New("飞书在线文档写入后回读不一致")
	}
	version := fmt.Sprint(verified.Revision)
	return domain.FeishuWriteResult{Node: updated, Version: &version, VerifiedDocument: &verified}, nil
}

func (g *feishuGateway) UploadFile(parent domain.FeishuWikiNode, workingDirectory, relativePath, title string, existing *domain.FeishuWikiNode, expectedVersion *domain.FeishuRemoteVersion) (domain.FeishuWriteResult, error) {
	root, err := filepath.Abs(workingDirectory)
	if err != nil {
		return domain.FeishuWriteResult{}, err
	}
	relative := filepath.Clean(filepath.FromSlash(relativePath))
	if relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return domain.FeishuWriteResult{}, errors.New("上传路径超出任务资料目录")
	}
	local := filepath.Join(root, relative)
	inside, err := filepath.Rel(root, local)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(os.PathSeparator)) || filepath.IsAbs(inside) {
		return domain.FeishuWriteResult{}, errors.New("上传路径超出任务资料目录")
	}
	info, err := os.Lstat(local)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return domain.FeishuWriteResult{}, errors.New("上传内容不是普通文件")
	}
	if existing != nil {
		if expectedVersion == nil || existing.ParentNodeToken != parent.NodeToken || existing.ObjectType != "file" {
			return domain.FeishuWriteResult{}, errors.New("覆盖文件缺少可信父节点或版本")
		}
	}
	var recoveredNode *domain.FeishuWikiNode
	var recoveredProof *domain.FeishuFileFingerprint
	policy := feishuInvocationPolicy{}
	if existing == nil {
		policy.beforeRetry = func() error { return g.verifyMaterialsNode(parent) }
	} else {
		policy.beforeAttempt = func() error { return g.verifyMaterialsUploadBaseline(*existing, *expectedVersion) }
	}
	if existing != nil || materialsCreationOperationTitle.MatchString(title) {
		policy.recoverWrite = func(_ *larkResponseError) (map[string]any, error) {
			if existing != nil {
				if err := g.verifyMaterialsNode(parent); err != nil {
					return nil, err
				}
				return nil, g.verifyMaterialsUploadBaseline(*existing, *expectedVersion)
			}
			node, proof, err := g.recoverMaterialsUpload(parent, title, local)
			if err != nil || node == nil {
				return nil, err
			}
			recoveredNode, recoveredProof = node, proof
			return map[string]any{"data": map[string]any{"file_token": node.ObjectToken, "version": proof.RemoteVersion.Version}}, nil
		}
	}
	args := []string{"drive", "+upload", "--file", filepath.ToSlash(relative), "--name", title, "--wiki-token", parent.NodeToken}
	if existing != nil {
		args = append(args, "--file-token", existing.ObjectToken)
	}
	args = append(args, "--as", "user", "--format", "json")
	response, err := g.invokeWithPolicy(args, nil, root, "上传飞书文件", 2*time.Minute, policy)
	if err != nil {
		return domain.FeishuWriteResult{}, err
	}
	data := responsePayload(response)
	token := text(data["file_token"])
	if token == "" {
		token = text(object(data["file"])["file_token"])
	}
	if token == "" {
		return domain.FeishuWriteResult{}, errors.New("上传飞书文件成功响应缺少 file_token")
	}
	if existing != nil && token != existing.ObjectToken {
		return domain.FeishuWriteResult{}, errors.New("覆盖飞书文件返回了不同的 file_token")
	}
	node := recoveredNode
	if node == nil {
		node, err = g.GetNode(token)
	}
	if err != nil || node == nil {
		if err == nil {
			err = errors.New("上传后无法读取飞书文件节点")
		}
		return domain.FeishuWriteResult{}, err
	}
	if node.ParentNodeToken != parent.NodeToken || node.SpaceID != parent.SpaceID || node.Title != title || node.ObjectType != "file" {
		return domain.FeishuWriteResult{}, errors.New("上传后的飞书文件标题或父节点与请求不一致")
	}
	if recoveredProof != nil {
		version := recoveredProof.RemoteVersion
		return domain.FeishuWriteResult{Node: *node, Version: &version.Version, CommentsFingerprint: &version.CommentsFingerprint}, nil
	}
	version, err := g.Version(*node, true)
	if err != nil {
		return domain.FeishuWriteResult{}, err
	}
	return domain.FeishuWriteResult{Node: *node, Version: &version.Version, CommentsFingerprint: &version.CommentsFingerprint}, nil
}

func (g *feishuGateway) Rename(node domain.FeishuWikiNode, title string) (domain.FeishuWriteResult, error) {
	if node.ObjectType == "file" {
		_, err := g.invoke([]string{"drive", "+update-title", "--token", node.NodeToken, "--type", "wiki", "--title", title, "--as", "user", "--format", "json"}, nil, "", "重命名飞书节点", 30*time.Second)
		if err != nil {
			return domain.FeishuWriteResult{}, err
		}
	} else {
		metadata, err := g.DocumentMetadata(node)
		if err != nil {
			return domain.FeishuWriteResult{}, err
		}
		paramsPath, cleanup, err := g.service.writeJSONArgument(map[string]int64{"document_revision_id": metadata.Revision})
		if err != nil {
			return domain.FeishuWriteResult{}, err
		}
		defer cleanup()
		body, _ := json.Marshal(map[string]any{"update_text_elements": map[string]any{"elements": []any{map[string]any{"text_run": map[string]string{"content": title}}}}})
		route := "/open-apis/docx/v1/documents/" + node.ObjectToken + "/blocks/" + node.ObjectToken
		if _, err := g.invoke([]string{"api", "PATCH", route, "--params", "@" + paramsPath, "--data", "-", "--as", "user"}, body, "", "重命名飞书在线文档标题", 30*time.Second); err != nil {
			return domain.FeishuWriteResult{}, err
		}
	}
	current, err := g.GetNode(node.NodeToken)
	if process.IsCleanupFailure(err) {
		return domain.FeishuWriteResult{}, err
	}
	if err != nil || current == nil || current.Title != title || current.ObjectToken != node.ObjectToken || current.ParentNodeToken != node.ParentNodeToken || current.SpaceID != node.SpaceID {
		return domain.FeishuWriteResult{}, errors.New("飞书节点重命名后回读不一致")
	}
	result := domain.FeishuWriteResult{Node: *current}
	if current.ObjectType == "docx" {
		metadata, err := g.DocumentMetadata(*current)
		if err != nil {
			return domain.FeishuWriteResult{}, err
		}
		version := fmt.Sprint(metadata.Revision)
		result.Version = &version
	}
	return result, nil
}

func (g *feishuGateway) DeleteNode(node domain.FeishuWikiNode) error {
	current, err := g.GetNode(node.NodeToken)
	if process.IsCleanupFailure(err) {
		return err
	}
	if err != nil || current == nil || *current != node {
		return errors.New("待删除飞书节点身份或位置已改变")
	}
	if node.ObjectType == "docx" {
		children, err := g.ListChildren(node)
		if err != nil {
			return err
		}
		if len(children) != 0 {
			return errors.New("飞书目录出现子节点，已取消删除以保护子节点")
		}
	}
	_, err = g.invoke([]string{"wiki", "+node-delete", "--node-token", node.NodeToken, "--obj-type", node.ObjectType, "--include-children=false", "--yes", "--as", "user", "--format", "json"}, nil, "", "删除已确认的飞书节点", 45*time.Second)
	if err != nil {
		return err
	}
	stillThere, err := g.GetNode(node.NodeToken)
	if err != nil {
		return err
	}
	if stillThere != nil {
		return errors.New("飞书删除命令返回成功，但节点仍然存在")
	}
	return nil
}

func (g *feishuGateway) discover(target domain.FeishuWikiNode, scope, requirementTitle string, receipt *domain.MaterialsBootstrapRecovery) (domain.MaterialsSyncDiscoverySnapshot, error) {
	targetChildren, err := g.ListChildren(target)
	if err != nil {
		return domain.MaterialsSyncDiscoverySnapshot{}, err
	}
	bootstrapRoot, err := g.materialsBootstrapRoot(target, requirementTitle, receipt, targetChildren)
	if err != nil {
		return domain.MaterialsSyncDiscoverySnapshot{}, err
	}
	for _, control := range targetChildren {
		if control.Title != materialsManifestTitle || control.ObjectType != "docx" {
			continue
		}
		doc, err := g.ReadDocument(control, false)
		if err != nil {
			return domain.MaterialsSyncDiscoverySnapshot{}, err
		}
		if strings.Contains(doc.Content, materialsLegacyMarker) {
			return domain.MaterialsSyncDiscoverySnapshot{}, errors.New("飞书同步配置 v1 不受支持，未迁移旧配置")
		}
		if strings.Contains(doc.Content, materialsManifestMarker) {
			return domain.MaterialsSyncDiscoverySnapshot{}, errors.New("v2 飞书同步配置必须位于需求根节点下")
		}
	}
	prefix := "SilverWing 候选需求根 " + scope[:16] + " ["
	managed := []domain.MaterialsSyncDiscoveryManifest{}
	for _, candidate := range targetChildren {
		if candidate.ObjectType != "docx" || (candidate.Title != requirementTitle && !strings.HasPrefix(candidate.Title, prefix)) {
			continue
		}
		children, err := g.ListChildren(candidate)
		if err != nil {
			return domain.MaterialsSyncDiscoverySnapshot{}, err
		}
		controls := []domain.FeishuWikiNode{}
		for _, node := range children {
			if node.Title == materialsManifestTitle && node.ObjectType == "docx" {
				controls = append(controls, node)
			}
		}
		if len(controls) == 0 {
			if bootstrapRoot != nil && candidate.NodeToken == bootstrapRoot.NodeToken {
				if len(children) != 0 {
					return domain.MaterialsSyncDiscoverySnapshot{}, errors.New("待恢复需求根包含未登记的子节点，已停止初始化")
				}
				continue
			}
			if strings.HasPrefix(candidate.Title, prefix) {
				return domain.MaterialsSyncDiscoverySnapshot{}, errors.New("发现未完成的候选需求根，没有可验证的同步配置；请执行恢复检查")
			}
			continue
		}
		if len(controls) != 1 {
			return domain.MaterialsSyncDiscoverySnapshot{}, errors.New("同一飞书需求根下同步配置不唯一")
		}
		control := controls[0]
		doc, err := g.ReadDocument(control, false)
		if err != nil {
			return domain.MaterialsSyncDiscoverySnapshot{}, err
		}
		if strings.Contains(doc.Content, materialsLegacyMarker) {
			return domain.MaterialsSyncDiscoverySnapshot{}, errors.New("飞书同步配置 v1 不受支持，未迁移旧配置")
		}
		if !strings.Contains(doc.Content, materialsManifestMarker) {
			if bootstrapRoot != nil && candidate.NodeToken == bootstrapRoot.NodeToken {
				return domain.MaterialsSyncDiscoverySnapshot{}, errors.New("待恢复需求根中的同步配置缺少管理标识")
			}
			continue
		}
		value, err := decodeMaterialsManifest(doc.Content)
		if err != nil {
			return domain.MaterialsSyncDiscoverySnapshot{}, err
		}
		if value.ScopeID != scope {
			if bootstrapRoot != nil && candidate.NodeToken == bootstrapRoot.NodeToken {
				return domain.MaterialsSyncDiscoverySnapshot{}, errors.New("待恢复需求根中的同步配置属于其他范围")
			}
			continue
		}
		if bootstrapRoot != nil && candidate.NodeToken == bootstrapRoot.NodeToken && value.OperationID != receipt.OperationID {
			return domain.MaterialsSyncDiscoverySnapshot{}, errors.New("待恢复需求根中的同步配置属于其他操作")
		}
		if err := validateRemoteManifest(value, target, candidate, control); err != nil {
			return domain.MaterialsSyncDiscoverySnapshot{}, err
		}
		managed = append(managed, domain.MaterialsSyncDiscoveryManifest{Node: control, Revision: doc.Revision, Value: value, RequirementRoot: candidate})
	}
	if receipt != nil {
		for _, item := range managed {
			if bootstrapRoot == nil || item.RequirementRoot.NodeToken != bootstrapRoot.NodeToken {
				return domain.MaterialsSyncDiscoverySnapshot{}, errors.New("已有受管需求根与本地初始化记录冲突，请核对后恢复")
			}
		}
		copy := *receipt
		copy.Root = bootstrapRoot
		receipt = &copy
	}
	return domain.MaterialsSyncDiscoverySnapshot{TargetNodeToken: target.NodeToken, SpaceID: target.SpaceID, ScopeID: scope, TargetChildren: targetChildren, Managed: managed, Bootstrap: receipt}, nil
}

func decodeMaterialsManifest(content string) (domain.MaterialsSyncManifest, error) {
	if strings.Contains(content, materialsLegacyMarker) {
		return domain.MaterialsSyncManifest{}, errors.New("飞书同步配置 v1 不受支持，未迁移旧配置")
	}
	if !strings.Contains(content, materialsManifestMarker) {
		return domain.MaterialsSyncManifest{}, errors.New("飞书同步配置缺少管理标识")
	}
	blocks := regexp.MustCompile("(?is)```(?:json)?\\s*\\n([\\s\\S]*?)\\n```").FindAllStringSubmatch(content, -1)
	if len(blocks) != 1 {
		return domain.MaterialsSyncManifest{}, errors.New("飞书同步配置 JSON 块缺失或损坏")
	}
	decoder := json.NewDecoder(strings.NewReader(blocks[0][1]))
	decoder.DisallowUnknownFields()
	var manifest domain.MaterialsSyncManifest
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("飞书同步配置损坏或版本不支持：%w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return manifest, errors.New("飞书同步配置 JSON 块包含多余内容")
	}
	if err := domain.ValidateMaterialsSyncManager(manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func validateRemoteManifest(value domain.MaterialsSyncManifest, target, root, control domain.FeishuWikiNode) error {
	if value.Namespace == "" || value.ScopeID == "" || value.OperationID == "" || value.TargetNodeToken != target.NodeToken || value.SpaceID != target.SpaceID || value.RequirementNodeToken != root.NodeToken || value.State != domain.MaterialsSyncReady && value.State != domain.MaterialsSyncInitializing {
		return errors.New("飞书同步配置不属于当前目标或需求根")
	}
	if control.ParentNodeToken != root.NodeToken || root.ParentNodeToken != target.NodeToken || root.SpaceID != target.SpaceID || control.SpaceID != target.SpaceID {
		return errors.New("飞书同步配置已移动或对象位置异常")
	}
	for source, binding := range value.Bindings {
		if source == "" || binding.SourceID != source || binding.RequirementID == "" || !strings.HasPrefix(binding.RequirementTitle, binding.RequirementID+"-") || binding.Subdirectory == "" {
			return errors.New("飞书同步绑定不完整")
		}
		if materialsHash(target.SpaceID+"\n"+target.NodeToken+"\n"+binding.RequirementID+"\n"+binding.RequirementTitle) != value.ScopeID || materialsHash(binding.RequirementID+"\n"+binding.RequirementTitle+"\n"+binding.Subdirectory) != source {
			return errors.New("飞书同步绑定的需求范围标识不一致")
		}
		for path, entry := range binding.Entries {
			if path != entry.Path || !validRemoteMaterialsPath(path) || entry.NodeToken == "" || entry.ObjectToken == "" || entry.ParentNodeToken == "" || entry.RemoteVersion.Version == "" {
				return errors.New("飞书同步配置包含非法路径或空资源标识")
			}
			if entry.ObjectType != "docx" && entry.ObjectType != "file" {
				return errors.New("飞书同步资源类型不受支持")
			}
		}
		rootEntry, ok := binding.Entries[materialsRequirementRoot]
		if !ok || rootEntry.NodeToken != root.NodeToken || rootEntry.ObjectToken != root.ObjectToken || rootEntry.ParentNodeToken != target.NodeToken || (rootEntry.Title != root.Title && !initializingMaterialsRootTitle(value, binding, rootEntry, root)) {
			return errors.New("飞书同步绑定缺少当前受保护的需求根节点")
		}
	}
	return nil
}

func validRemoteMaterialsPath(path string) bool {
	if path == materialsRequirementRoot || path == materialsFilesRoot {
		return true
	}
	return path != "" && !strings.HasPrefix(path, "/") && !strings.ContainsAny(path, `\:`) && !strings.Contains(path, "//") && !strings.Contains(path, "..")
}

func (g *feishuGateway) CreateManifest(root domain.FeishuWikiNode, value domain.MaterialsSyncManifest, writeRoot string) (domain.FeishuWikiNode, int64, error) {
	content := encodeMaterialsManifest(value)
	var created domain.FeishuWriteResult
	var err error
	if g.SupportsCompleteDocumentCreation(content) {
		created, err = g.CreateCompleteDocument(root, materialsManifestTitle, content, writeRoot)
	} else {
		created, err = g.CreateDocument(root, materialsManifestTitle, content, writeRoot, false)
	}
	if err != nil {
		if process.IsCleanupFailure(err) {
			return domain.FeishuWikiNode{}, 0, err
		}
		if ctxErr := g.ctx.Err(); ctxErr != nil {
			return domain.FeishuWikiNode{}, 0, ctxErr
		}
		var unconfirmed *materialsUnconfirmedCreationError
		if errors.As(err, &unconfirmed) {
			return domain.FeishuWikiNode{}, 0, err
		}
		recovered, revision, recoveryErr := g.recoverMaterialsControl(root, value)
		if process.IsCleanupFailure(recoveryErr) {
			return domain.FeishuWikiNode{}, 0, recoveryErr
		}
		if ctxErr := g.ctx.Err(); ctxErr != nil {
			return domain.FeishuWikiNode{}, 0, ctxErr
		}
		if recoveryErr == nil && recovered != nil {
			return *recovered, revision, nil
		}
		return domain.FeishuWikiNode{}, 0, err
	}
	doc, err := g.ReadDocument(created.Node, false)
	if process.IsCleanupFailure(err) {
		return domain.FeishuWikiNode{}, 0, err
	}
	if err != nil || doc.Content != strings.TrimSpace(content) {
		return domain.FeishuWikiNode{}, 0, errors.New("飞书同步配置创建后回读不一致")
	}
	return created.Node, doc.Revision, nil
}

func encodeMaterialsManifest(value domain.MaterialsSyncManifest) string {
	data, _ := json.MarshalIndent(value, "", "  ")
	return materialsManifestMarker + "\n\n此文档记录 SilverWing 创建的资源，请保留完整配置。\n\n```json\n" + string(data) + "\n```"
}

func parseFeishuNode(value map[string]any) (domain.FeishuWikiNode, error) {
	data := responsePayload(value)
	for _, key := range []string{"node", "wiki_node", "wikiNode"} {
		if node := object(data[key]); node != nil {
			data = node
			break
		}
	}
	return parseFeishuNodeObject(data, value)
}

func parseFeishuNodeObject(data, envelope map[string]any) (domain.FeishuWikiNode, error) {
	node := domain.FeishuWikiNode{SpaceID: firstFieldText(data, "space_id", "spaceId"), NodeToken: firstFieldText(data, "node_token", "nodeToken"), ObjectToken: firstFieldText(data, "obj_token", "object_token", "objectToken"), ObjectType: strings.ToLower(firstFieldText(data, "obj_type", "object_type", "objectType")), ParentNodeToken: firstFieldText(data, "parent_node_token", "parentNodeToken"), Title: firstFieldText(data, "title", "node_title")}
	if node.SpaceID == "" {
		node.SpaceID = firstFieldText(responsePayload(envelope), "resolved_space_id")
	}
	if _, exists := data["parent_node_token"]; !exists {
		return node, errors.New("飞书知识库节点响应缺少 parent_node_token")
	}
	if node.NodeToken == "" || node.ObjectToken == "" || node.ObjectType == "" || node.SpaceID == "" || node.Title == "" {
		return node, errors.New("飞书知识库节点响应缺少 token、类型、空间或标题")
	}
	return node, nil
}

func responsePayload(value map[string]any) map[string]any {
	data := object(value["data"])
	if data != nil {
		return data
	}
	return value
}

func stripMarkdownDocumentTitle(content, title string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	isTitle := len(lines) > 0 && strings.TrimSpace(lines[0]) == "# "+title
	if len(lines) > 0 && strings.HasPrefix(lines[0], "<title>") && strings.HasSuffix(lines[0], "</title>") {
		encoded := strings.TrimSuffix(strings.TrimPrefix(lines[0], "<title>"), "</title>")
		var decoded struct {
			Text string `xml:",chardata"`
		}
		if !strings.Contains(encoded, "<") && xml.Unmarshal([]byte(lines[0]), &decoded) == nil && decoded.Text == title {
			isTitle = true
		}
	}
	if isTitle {
		lines = lines[1:]
		if len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
			lines = lines[1:]
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func markdownWithTitle(title, markdown string) string {
	return "# " + title + "\n\n" + strings.TrimSpace(markdown)
}

var _ = regexp.MustCompile
