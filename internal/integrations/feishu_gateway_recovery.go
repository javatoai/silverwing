package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type feishuInvocationPolicy struct {
	beforeAttempt func() error
	beforeRetry   func() error
	recoverWrite  func(*larkResponseError) (map[string]any, error)
	noRetryCode   int64
}

var materialsCreationOperationTitle = regexp.MustCompile(` \[sw-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\]$`)
var materialsUnsupportedCompleteMarkdown = regexp.MustCompile(`(?i)!\[|<\s*(?:img|image|iframe|attach|video|audio|object|embed|source|picture|whiteboard|html5-block)\b`)

func (e *larkResponseError) ambiguousCreateFailure() bool {
	return e.timedOut || e.code != nil && *e.code == 131001 || e.retryable && e.subtype == "server_error"
}

func (e *larkResponseError) retryableUploadFailure() bool {
	return e.retryable && e.subtype == "server_error" && e.code != nil && *e.code == 233523001
}

func waitLarkUploadRecovery(ctx context.Context, retryNumber int, retryAfter time.Duration) error {
	delay := retryAfter
	if delay <= 0 {
		delay = time.Duration(retryNumber)*time.Second + time.Duration(rand.Int63n(int64(retryNumber)*1000))*time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

type materialsUnconfirmedCreationError struct {
	message string
	cause   error
}

func (e *materialsUnconfirmedCreationError) Error() string { return e.message }
func (e *materialsUnconfirmedCreationError) Unwrap() error { return e.cause }

func (g *feishuGateway) unconfirmedMaterialsCreate(description string, failure, recoveryError error) error {
	if process.IsCleanupFailure(failure) {
		return failure
	}
	if process.IsCleanupFailure(recoveryError) {
		return recoveryError
	}
	if err := g.ctx.Err(); err != nil {
		return err
	}
	return &materialsUnconfirmedCreationError{
		message: "飞书" + description + "创建结果无法确认，已保留同步记录",
		cause:   errors.Join(failure, recoveryError),
	}
}

func (g *feishuGateway) verifyMaterialsNode(node domain.FeishuWikiNode) error {
	if err := g.ctx.Err(); err != nil {
		return err
	}
	current, err := g.GetNode(node.NodeToken)
	if err != nil {
		return err
	}
	if current == nil || *current != node {
		return errors.New("飞书节点的身份、位置或标题已改变，已停止重试")
	}
	return nil
}

func (g *feishuGateway) uniqueMaterialsCreation(parent domain.FeishuWikiNode, title, objectType string) (*domain.FeishuWikiNode, error) {
	if err := g.verifyMaterialsNode(parent); err != nil {
		return nil, err
	}
	children, err := g.ListChildren(parent)
	if err != nil {
		return nil, err
	}
	var found *domain.FeishuWikiNode
	for _, node := range children {
		if node.Title != title {
			continue
		}
		if found != nil {
			return nil, errors.New("同一创建操作出现多个远端资源，无法安全恢复")
		}
		copy := node
		found = &copy
	}
	if found != nil && (found.NodeToken == "" || found.ObjectToken == "" || found.SpaceID != parent.SpaceID || found.ParentNodeToken != parent.NodeToken || found.ObjectType != objectType) {
		return nil, errors.New("创建恢复候选的类型、身份或位置不匹配")
	}
	return found, nil
}

func (g *feishuGateway) recoverAmbiguousMaterialsCreate(parent domain.FeishuWikiNode, title, description string, failure error) (domain.FeishuWriteResult, error) {
	if process.IsCleanupFailure(failure) {
		return domain.FeishuWriteResult{}, failure
	}
	if err := g.ctx.Err(); err != nil {
		return domain.FeishuWriteResult{}, err
	}
	node, err := g.uniqueMaterialsCreation(parent, title, "docx")
	if err != nil || node == nil {
		if err == nil {
			err = fmt.Errorf("%s创建结果无法通过唯一操作标题确认", description)
		}
		return domain.FeishuWriteResult{}, g.unconfirmedMaterialsCreate(description, failure, err)
	}
	return domain.FeishuWriteResult{Node: *node}, nil
}

func (g *feishuGateway) SupportsCompleteDocumentCreation(markdown string) bool {
	return !materialsUnsupportedCompleteMarkdown.MatchString(markdown)
}

func (g *feishuGateway) CreateCompleteDocument(parent domain.FeishuWikiNode, title, markdown, workingDirectory string) (domain.FeishuWriteResult, error) {
	if strings.TrimSpace(title) == "" {
		return domain.FeishuWriteResult{}, errors.New("飞书文档标题不能为空")
	}
	if !g.SupportsCompleteDocumentCreation(markdown) {
		return domain.FeishuWriteResult{}, errors.New("包含图片或嵌入资源的 Markdown 必须使用资源同步路径")
	}
	escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(title)
	request, _ := json.Marshal(map[string]string{
		"format": "markdown", "content": "<title>" + escaped + "</title>\n" + markdown,
		"extra_param": `{"open_create_async":false}`, "parent_token": parent.NodeToken,
	})
	response, err := g.invokeWithPolicy([]string{"api", "POST", "/open-apis/docs_ai/v1/documents", "--data", "-", "--as", "user"}, request, workingDirectory, "同步创建飞书在线文档", 10*time.Minute,
		feishuInvocationPolicy{noRetryCode: 10020, beforeRetry: func() error { return g.verifyMaterialsNode(parent) }})
	if err != nil {
		if process.IsCleanupFailure(err) {
			return domain.FeishuWriteResult{}, err
		}
		var failure *larkResponseError
		if errors.As(err, &failure) && (failure.ambiguousCreateFailure() || failure.code != nil && *failure.code == 10020 || failure.code == nil && !failure.timedOut) {
			return g.recoverOrRefuseCompleteCreate(parent, title, err, nil)
		}
		return domain.FeishuWriteResult{}, err
	}
	data := responsePayload(response)
	if data["task"] != nil || data["task_id"] != nil {
		return g.recoverOrRefuseCompleteCreate(parent, title, errors.New("同步创建飞书在线文档未返回最终文档对象"), nil)
	}
	document := object(data["document"])
	token := text(document["document_id"])
	if token == "" {
		return g.recoverOrRefuseCompleteCreate(parent, title, errors.New("同步创建飞书在线文档成功响应缺少 document_id"), nil)
	}
	var revision *string
	if value, ok := integer(document["revision_id"]); ok && value >= 0 {
		version := fmt.Sprint(value)
		revision = &version
	}
	node, err := g.GetNode(token)
	if process.IsCleanupFailure(err) {
		return domain.FeishuWriteResult{}, err
	}
	if err != nil || node == nil || node.SpaceID != parent.SpaceID || node.ParentNodeToken != parent.NodeToken || node.Title != title || node.ObjectType != "docx" || node.ObjectToken != token {
		if err := g.ctx.Err(); err != nil {
			return domain.FeishuWriteResult{}, err
		}
		failure := errors.New("飞书在线文档已创建，但节点回执暂不可用或不匹配")
		recovered, recoveryErr := g.recoverOrRefuseCompleteCreate(parent, title, failure, revision)
		if recoveryErr != nil {
			return domain.FeishuWriteResult{}, recoveryErr
		}
		if recovered.Node.ObjectToken != token {
			return domain.FeishuWriteResult{}, g.unconfirmedMaterialsCreate("在线文档", failure, errors.New("创建回执与唯一操作节点不一致"))
		}
		return recovered, nil
	}
	return domain.FeishuWriteResult{Node: *node, Version: revision}, nil
}

func (g *feishuGateway) recoverOrRefuseCompleteCreate(parent domain.FeishuWikiNode, title string, failure error, revision *string) (domain.FeishuWriteResult, error) {
	if process.IsCleanupFailure(failure) {
		return domain.FeishuWriteResult{}, failure
	}
	if err := g.ctx.Err(); err != nil {
		return domain.FeishuWriteResult{}, err
	}
	if !materialsCreationOperationTitle.MatchString(title) {
		return domain.FeishuWriteResult{}, g.unconfirmedMaterialsCreate("在线文档", failure, nil)
	}
	recovered, err := g.recoverAmbiguousMaterialsCreate(parent, title, "在线文档", failure)
	if err == nil {
		recovered.Version = revision
	}
	return recovered, err
}

func (g *feishuGateway) verifyMaterialsUploadBaseline(node domain.FeishuWikiNode, expected domain.FeishuRemoteVersion) error {
	if err := g.verifyMaterialsNode(node); err != nil {
		return err
	}
	version, err := g.Version(node, true)
	if err != nil {
		return err
	}
	if version != expected {
		return errors.New("飞书文件版本或评论已变化，已取消覆盖")
	}
	return nil
}

func (g *feishuGateway) recoverMaterialsUpload(parent domain.FeishuWikiNode, title, localPath string) (*domain.FeishuWikiNode, *domain.FeishuFileFingerprint, error) {
	node, err := g.uniqueMaterialsCreation(parent, title, "file")
	if err != nil || node == nil {
		return nil, nil, err
	}
	proof, err := g.ReadFileFingerprint(*node)
	if err != nil {
		return nil, nil, err
	}
	if err := g.ctx.Err(); err != nil {
		return nil, nil, err
	}
	local, err := os.Open(localPath)
	if err != nil {
		return nil, nil, err
	}
	defer local.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, local); err != nil {
		return nil, nil, err
	}
	if proof.SHA256 != hex.EncodeToString(hash.Sum(nil)) || proof.RemoteVersion.CommentsFingerprint != materialsHash("[]") {
		return nil, nil, errors.New("未确认上传的文件内容或评论已变化，已保留恢复记录")
	}
	return node, &proof, nil
}

func materialsCompleteCreationWave(plan domain.MaterialsSyncPlan, path string) bool {
	depth := strings.Count(path, "/")
	for _, item := range plan.Items {
		if item.Path != materialsRequirementRoot && item.Path != materialsFilesRoot && item.Action == domain.MaterialsSyncCreate &&
			strings.Count(item.Path, "/") == depth && item.Kind != domain.MaterialsSyncMarkdown {
			return false
		}
	}
	return true
}

func (g *feishuGateway) recoverMaterialsControl(root domain.FeishuWikiNode, expected domain.MaterialsSyncManifest) (*domain.FeishuWikiNode, int64, error) {
	if err := g.ctx.Err(); err != nil {
		return nil, 0, err
	}
	children, err := g.ListChildren(root)
	if err != nil {
		return nil, 0, err
	}
	var found *domain.FeishuWikiNode
	var revision int64
	target := domain.FeishuWikiNode{NodeToken: expected.TargetNodeToken, SpaceID: expected.SpaceID}
	for _, node := range children {
		if node.Title != materialsManifestTitle || node.ObjectType != "docx" {
			continue
		}
		doc, err := g.ReadDocument(node, false)
		if err != nil {
			return nil, 0, err
		}
		if !strings.Contains(doc.Content, materialsManifestMarker) && !strings.Contains(doc.Content, materialsLegacyMarker) {
			continue
		}
		value, err := decodeMaterialsManifest(doc.Content)
		if err != nil {
			return nil, 0, err
		}
		if err := validateRemoteManifest(value, target, root, node); err != nil {
			return nil, 0, err
		}
		if value.ScopeID != expected.ScopeID || value.OperationID != expected.OperationID {
			continue
		}
		if found != nil {
			return nil, 0, errors.New("同一创建操作出现多个飞书同步配置")
		}
		if doc.Content != strings.TrimSpace(encodeMaterialsManifest(expected)) {
			return nil, 0, errors.New("同步配置创建结果与初始化记录不一致")
		}
		copy := node
		found, revision = &copy, doc.Revision
	}
	return found, revision, nil
}
