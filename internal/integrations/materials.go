package integrations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	path "path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

const (
	materialsManifestTitle   = "SilverWing 同步配置"
	materialsManifestMarker  = "SilverWing materials sync manifest v2"
	materialsLegacyMarker    = "SilverWing materials sync manifest v1"
	materialsRequirementRoot = "/requirement"
	materialsFilesRoot       = "/materials"
)

type localMaterial struct {
	path        string
	kind        domain.MaterialsSyncKind
	hash        string
	linkedPaths []string
}

type localMaterialScan struct {
	root             string
	files            map[string]localMaterial
	allFiles         map[string]localMaterial
	fingerprint      string
	blocked          []string
	citationMarkdown map[string]bool
}

type MaterialsSyncExecution struct {
	Result                  domain.MaterialsSyncResult
	RequirementDirectoryURL string
}

func (s *Service) resolveMaterialsDirectory(ctx context.Context, config domain.AppConfig, payload map[string]any) (any, error) {
	targetURL := stringValue(payload, "targetUrl")
	if targetURL == "" && config.RequirementMaterialsFeishuTarget != nil {
		targetURL = strings.TrimSpace(*config.RequirementMaterialsFeishuTarget)
	}
	requirementID := stringValue(payload, "requirementId", "id")
	title := stringValue(payload, "requirementTitle", "title")
	if requirementID == "" || !numericIDPattern.MatchString(requirementID) || title == "" || !strings.HasPrefix(title, requirementID+"-") {
		return nil, errors.New("当前需求编号或飞书需求目录名称无效")
	}
	gateway := newFeishuGateway(ctx, s, config)
	target, err := gateway.ResolveTarget(targetURL)
	if err != nil {
		return nil, err
	}
	scope := materialsHash(target.SpaceID + "\n" + target.NodeToken + "\n" + requirementID + "\n" + title)
	discovery, err := s.discoverMaterials(gateway, target, scope, title)
	if err != nil {
		return nil, err
	}
	if len(discovery.Managed) > 1 {
		return nil, errors.New("飞书目标下存在多个同范围同步根，无法确定资料目录")
	}
	if len(discovery.Managed) == 0 {
		return map[string]any{"found": false, "url": ""}, nil
	}
	root := discovery.Managed[0].RequirementRoot
	return map[string]any{"found": true, "url": materialsWikiDirectoryURL(targetURL, root.NodeToken)}, nil
}

func (s *Service) resolveMaterialsEntry(ctx context.Context, config domain.AppConfig, payload map[string]any) (any, error) {
	targetURL := stringValue(payload, "targetUrl")
	if targetURL == "" && config.RequirementMaterialsFeishuTarget != nil {
		targetURL = strings.TrimSpace(*config.RequirementMaterialsFeishuTarget)
	}
	validatedTarget, err := domain.ValidateMaterialsFeishuTarget(targetURL)
	if err != nil || validatedTarget == nil {
		return nil, errors.New("飞书同步目标地址无效")
	}
	requirementID := stringValue(payload, "requirementId", "id")
	title := stringValue(payload, "requirementTitle", "title")
	if requirementID == "" || !numericIDPattern.MatchString(requirementID) || title == "" || !strings.HasPrefix(title, requirementID+"-") {
		return nil, errors.New("当前需求编号或飞书需求目录名称无效")
	}
	rawPath, ok := payload["relativePath"].(string)
	if !ok {
		return nil, errors.New("资料相对路径无效")
	}
	relativePath, err := validateMaterialsRelativePath(rawPath)
	if err != nil {
		return nil, err
	}
	subdirectory := "需求资料"
	if config.RequirementMaterialsSubdirectory != nil && strings.TrimSpace(*config.RequirementMaterialsSubdirectory) != "" {
		subdirectory = strings.TrimSpace(*config.RequirementMaterialsSubdirectory)
	}
	if err := validateMaterialsSubdirectory(subdirectory); err != nil {
		return nil, err
	}
	gateway := newFeishuGateway(ctx, s, config)
	target, err := gateway.ResolveTarget(*validatedTarget)
	if err != nil {
		return nil, err
	}
	scope := materialsHash(target.SpaceID + "\n" + target.NodeToken + "\n" + requirementID + "\n" + title)
	discovery, err := s.discoverMaterials(gateway, target, scope, title)
	if err != nil {
		return nil, err
	}
	if len(discovery.Managed) > 1 {
		return nil, errors.New("飞书目标下存在多个同范围同步根，无法确定资料归属")
	}
	if len(discovery.Managed) == 0 {
		return map[string]any{"found": false, "url": ""}, nil
	}
	managed := discovery.Managed[0]
	source := materialsHash(requirementID + "\n" + title + "\n" + subdirectory)
	binding, ok := managed.Value.Bindings[source]
	if !ok || binding.Entries == nil {
		return map[string]any{"found": false, "url": ""}, nil
	}
	if _, ok := binding.Entries[materialsFilesRoot]; !ok {
		return map[string]any{"found": false, "url": ""}, nil
	}
	entry, ok := binding.Entries[relativePath]
	if !ok || entry.Kind == domain.MaterialsSyncDirectory {
		return map[string]any{"found": false, "url": ""}, nil
	}
	if entry.Kind == domain.MaterialsSyncMarkdown && entry.ObjectType != "docx" || entry.Kind == domain.MaterialsSyncFile && entry.ObjectType != "file" {
		return nil, errors.New("飞书资料类型与同步登记不一致")
	}
	node, err := gateway.ownedMaterialNode(target, binding, entry)
	if err != nil {
		return nil, err
	}
	if node.NodeToken == "" || node.NodeToken == target.NodeToken || node.ParentNodeToken != entry.ParentNodeToken || node.SpaceID != target.SpaceID {
		return map[string]any{"found": false, "url": ""}, nil
	}
	return map[string]any{"found": true, "url": materialsWikiDirectoryURL(*validatedTarget, node.NodeToken)}, nil
}

func (s *Service) ensureMaterials(ctx context.Context, config domain.AppConfig, payload map[string]any) (any, error) {
	input := stringValue(payload, "requirementInput", "requirementLink", "requirementId", "requirement")
	folder := stringValue(payload, "folderName", "directoryFolderName", "taskName")
	return s.prep.Ensure(ctx, config, input, folder)
}

func (s *Service) materialRequest(config domain.AppConfig, payload map[string]any) (domain.MaterialsSyncRequest, error) {
	target := stringValue(payload, "targetUrl")
	if target == "" && config.RequirementMaterialsFeishuTarget != nil {
		target = strings.TrimSpace(*config.RequirementMaterialsFeishuTarget)
	}
	if _, err := domain.ValidateMaterialsFeishuTarget(target); err != nil {
		return domain.MaterialsSyncRequest{}, err
	}
	root := stringValue(payload, "writeRoot", "path")
	if root == "" {
		return domain.MaterialsSyncRequest{}, errors.New("缺少本地任务资料目录")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return domain.MaterialsSyncRequest{}, err
	}
	id := stringValue(payload, "requirementId", "id")
	if id == "" {
		id = requirementIDFromLink(stringValue(payload, "requirementLink", "url"))
	}
	title := stringValue(payload, "requirementTitle", "title", "directoryFolderName")
	subdirectory := stringValue(payload, "subdirectory")
	if subdirectory == "" && config.RequirementMaterialsSubdirectory != nil {
		subdirectory = strings.TrimSpace(*config.RequirementMaterialsSubdirectory)
	}
	if subdirectory == "" {
		subdirectory = "需求资料"
	}
	if !numericIDPattern.MatchString(id) || title == "" || !strings.HasPrefix(title, id+"-") {
		return domain.MaterialsSyncRequest{}, errors.New("当前需求编号或需求目录名称无效")
	}
	if err := validateMaterialsSubdirectory(subdirectory); err != nil {
		return domain.MaterialsSyncRequest{}, err
	}
	selectedPaths, err := selectedMaterialsPaths(payload)
	if err != nil {
		return domain.MaterialsSyncRequest{}, err
	}
	return domain.MaterialsSyncRequest{TargetURL: target, WriteRoot: absolute, RequirementID: id, RequirementTitle: title, Subdirectory: subdirectory, SelectedPaths: selectedPaths}, nil
}

func selectedMaterialsPaths(payload map[string]any) ([]string, error) {
	value, exists := payload["selectedPaths"]
	if !exists || value == nil {
		return nil, nil
	}
	var values []string
	switch items := value.(type) {
	case []string:
		values = append(values, items...)
	case []any:
		values = make([]string, 0, len(items))
		for _, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, errors.New("selectedPaths 必须只包含资料相对路径")
			}
			values = append(values, text)
		}
	default:
		return nil, errors.New("selectedPaths 必须是资料相对路径数组")
	}
	unique := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized, err := validateMaterialsRelativePath(value)
		if err != nil {
			return nil, err
		}
		if _, exists := unique[normalized]; exists {
			continue
		}
		unique[normalized] = struct{}{}
		result = append(result, normalized)
	}
	sort.Strings(result)
	return result, nil
}

func validateMaterialsRelativePath(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, `\\:`) || strings.ContainsRune(value, '\x00') || path.IsAbs(value) || path.Clean(value) != value || value == "." {
		return "", errors.New("资料相对路径无效")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("资料相对路径无效")
		}
	}
	return value, nil
}

func requirementIDFromLink(raw string) string {
	link := domain.ParseFeishuWorkItemLink(raw)
	if link != nil {
		return link.WorkItemID
	}
	if numericIDPattern.MatchString(strings.TrimSpace(raw)) {
		return strings.TrimSpace(raw)
	}
	return ""
}

func validateMaterialsSubdirectory(value string) error {
	if value == "" || value == "." || value == ".." || strings.TrimSpace(value) != value || strings.ContainsAny(value, `/\<>:"|?*`) || strings.HasSuffix(value, ".") || strings.HasSuffix(value, " ") {
		return errors.New("资料子目录名称无效")
	}
	base := strings.TrimSuffix(strings.ToUpper(value), filepath.Ext(value))
	if regexp.MustCompile(`^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$`).MatchString(base) {
		return errors.New("资料子目录不能使用 Windows 保留名称")
	}
	return nil
}

func (s *Service) planMaterials(ctx context.Context, config domain.AppConfig, payload map[string]any) (any, error) {
	request, err := s.materialRequest(config, payload)
	if err != nil {
		return nil, err
	}
	plan, err := s.buildMaterialsPlan(ctx, config, request)
	if err != nil {
		return nil, err
	}
	if err := s.saveMaterialsPlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func (s *Service) buildMaterialsPlan(ctx context.Context, config domain.AppConfig, request domain.MaterialsSyncRequest) (domain.MaterialsSyncPlan, error) {
	scan, err := scanMaterialsForRequest(request.WriteRoot, request.SelectedPaths)
	if err != nil {
		return domain.MaterialsSyncPlan{}, err
	}
	gateway := newFeishuGateway(ctx, s, config)
	target, err := gateway.ResolveTarget(request.TargetURL)
	if err != nil {
		return domain.MaterialsSyncPlan{}, err
	}
	scope := materialsHash(target.SpaceID + "\n" + target.NodeToken + "\n" + request.RequirementID + "\n" + request.RequirementTitle)
	source := materialsHash(request.RequirementID + "\n" + request.RequirementTitle + "\n" + request.Subdirectory)
	discovery, err := s.discoverMaterials(gateway, target, scope, request.RequirementTitle)
	if err != nil {
		return domain.MaterialsSyncPlan{}, err
	}
	if len(discovery.Managed) > 1 {
		return domain.MaterialsSyncPlan{}, errors.New("飞书目标下存在多个同范围同步根，无法确定归属")
	}
	var manifestNode *domain.FeishuWikiNode
	var manifestRevision *int64
	var manifest *domain.MaterialsSyncManifest
	var requirementRoot *domain.FeishuWikiNode
	if len(discovery.Managed) == 1 {
		managed := discovery.Managed[0]
		manifestNode, manifestRevision, manifest, requirementRoot = &managed.Node, &managed.Revision, &managed.Value, &managed.RequirementRoot
		if managed.Value.State != domain.MaterialsSyncReady && managed.Value.State != domain.MaterialsSyncInitializing {
			return domain.MaterialsSyncPlan{}, errors.New("飞书同步清单状态不受支持")
		}
	}
	items := []domain.MaterialsSyncItem{}
	var binding *domain.MaterialsSyncBinding
	if manifest != nil {
		if existing, ok := manifest.Bindings[source]; ok {
			binding = &existing
		}
	}
	if binding != nil {
		for _, pending := range binding.Pending {
			if !materialsPlanIncludesPath(request, scan, pending.Path) {
				continue
			}
			items = append(items, domain.MaterialsSyncItem{Path: pending.Path, Kind: pending.Kind, Action: domain.MaterialsSyncConflict, Reason: nonblank("存在上次未确认的远端操作；请先核对操作记录并恢复，当前计划不会自动重试"), Entry: bindingEntry(binding, pending.Path)})
		}
		for path, entry := range binding.Entries {
			if path == materialsRequirementRoot || path == materialsFilesRoot {
				continue
			}
			if !materialsPlanIncludesPath(request, scan, path) {
				continue
			}
			if _, present := scan.files[path]; present {
				continue
			}
			node, nodeErr := gateway.GetNode(entry.NodeToken)
			if nodeErr != nil {
				return domain.MaterialsSyncPlan{}, nodeErr
			}
			if node == nil {
				items = append(items, domain.MaterialsSyncItem{Path: path, Kind: entry.Kind, Action: domain.MaterialsSyncDelete, Reason: nonblank("远端已不存在"), Entry: &entry})
				continue
			}
			if _, ownedErr := gateway.ownedMaterialNode(target, *binding, entry); ownedErr != nil {
				if process.IsCleanupFailure(ownedErr) {
					return domain.MaterialsSyncPlan{}, ownedErr
				}
				if ctx.Err() != nil {
					return domain.MaterialsSyncPlan{}, ctx.Err()
				}
				items = append(items, domain.MaterialsSyncItem{Path: path, Kind: entry.Kind, Action: domain.MaterialsSyncConflict, Reason: nonblank(ownedErr.Error()), Entry: &entry, ObservedNode: node})
				continue
			}
			if !materialsSameIdentity(*node, entry, target.SpaceID) {
				items = append(items, domain.MaterialsSyncItem{Path: path, Kind: entry.Kind, Action: domain.MaterialsSyncConflict, Reason: nonblank("登记的远端资源已删除、移动或改变；为防止误删不自动重建"), Entry: &entry})
				continue
			}
			current, versionErr := gateway.Version(*node, true)
			if versionErr != nil {
				return domain.MaterialsSyncPlan{}, versionErr
			}
			if node.Title != entry.Title {
				items = append(items, domain.MaterialsSyncItem{Path: path, Kind: entry.Kind, Action: domain.MaterialsSyncConflict, Reason: nonblank("远端标题被人工修改"), Entry: &entry, ObservedVersion: &current, ObservedNode: node})
			} else if current != entry.RemoteVersion {
				items = append(items, domain.MaterialsSyncItem{Path: path, Kind: entry.Kind, Action: domain.MaterialsSyncConflict, Reason: nonblank("远端资源版本或评论已改变；请核对后选择覆盖"), Entry: &entry, ObservedVersion: &current, ObservedNode: node})
			} else {
				items = append(items, domain.MaterialsSyncItem{Path: path, Kind: entry.Kind, Action: domain.MaterialsSyncDelete, Entry: &entry, ObservedVersion: &current, ObservedNode: node})
			}
		}
	}
	paths := make([]string, 0, len(scan.files))
	for path := range scan.files {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		depthI, depthJ := strings.Count(paths[i], "/"), strings.Count(paths[j], "/")
		if depthI != depthJ {
			return depthI < depthJ
		}
		return paths[i] < paths[j]
	})
	for _, path := range paths {
		file := scan.files[path]
		entry, exists := domain.MaterialsSyncEntry{}, false
		if binding != nil {
			entry, exists = binding.Entries[path]
		}
		if !exists {
			items = append(items, domain.MaterialsSyncItem{Path: path, Kind: file.kind, Action: domain.MaterialsSyncCreate, LocalHash: file.hash})
			continue
		}
		node, nodeErr := gateway.GetNode(entry.NodeToken)
		if nodeErr != nil {
			return domain.MaterialsSyncPlan{}, nodeErr
		}
		if node == nil || !materialsSameIdentity(*node, entry, target.SpaceID) {
			items = append(items, domain.MaterialsSyncItem{Path: path, Kind: file.kind, Action: domain.MaterialsSyncConflict, LocalHash: file.hash, Reason: nonblank("登记的远端资源已删除、移动或改变；为防止覆盖错误资源已停止"), Entry: &entry})
			continue
		}
		if _, ownedErr := gateway.ownedMaterialNode(target, *binding, entry); ownedErr != nil {
			if process.IsCleanupFailure(ownedErr) {
				return domain.MaterialsSyncPlan{}, ownedErr
			}
			if ctx.Err() != nil {
				return domain.MaterialsSyncPlan{}, ctx.Err()
			}
			items = append(items, domain.MaterialsSyncItem{Path: path, Kind: file.kind, Action: domain.MaterialsSyncConflict, LocalHash: file.hash, Reason: nonblank(ownedErr.Error()), Entry: &entry, ObservedNode: node})
			continue
		}
		current, versionErr := gateway.Version(*node, true)
		if versionErr != nil {
			return domain.MaterialsSyncPlan{}, versionErr
		}
		if node.Title != entry.Title {
			items = append(items, domain.MaterialsSyncItem{Path: path, Kind: file.kind, Action: domain.MaterialsSyncConflict, LocalHash: file.hash, Reason: nonblank("远端标题被人工修改"), Entry: &entry, ObservedNode: node, ObservedVersion: &current})
		} else if file.kind != entry.Kind {
			items = append(items, domain.MaterialsSyncItem{Path: path, Kind: file.kind, Action: domain.MaterialsSyncConflict, LocalHash: file.hash, Reason: nonblank("本地文件类型与登记类型不同"), Entry: &entry, ObservedNode: node, ObservedVersion: &current})
		} else if current != entry.RemoteVersion {
			items = append(items, domain.MaterialsSyncItem{Path: path, Kind: file.kind, Action: domain.MaterialsSyncConflict, LocalHash: file.hash, Reason: nonblank("远端资源版本或评论已改变；请核对后选择覆盖"), Entry: &entry, ObservedVersion: &current, ObservedNode: node})
		} else if file.hash == entry.LocalHash {
			items = append(items, domain.MaterialsSyncItem{Path: path, Kind: file.kind, Action: domain.MaterialsSyncSkip, LocalHash: file.hash, Entry: &entry, ObservedVersion: &current, ObservedNode: node})
		} else {
			items = append(items, domain.MaterialsSyncItem{Path: path, Kind: file.kind, Action: domain.MaterialsSyncUpdate, LocalHash: file.hash, Entry: &entry, ObservedVersion: &current, ObservedNode: node})
		}
	}
	if len(scan.blocked) > 0 {
		items = append(items, domain.MaterialsSyncItem{Path: "", Kind: domain.MaterialsSyncFile, Action: domain.MaterialsSyncConflict, Reason: nonblank("扫描发现链接或特殊文件，已停止删除：" + strings.Join(scan.blocked, ", "))})
		for index := range items {
			if items[index].Action == domain.MaterialsSyncDelete {
				items[index].Action = domain.MaterialsSyncConflict
				items[index].Reason = nonblank("扫描发现链接或特殊文件，已停止删除")
			}
		}
	}
	plan := domain.MaterialsSyncPlan{ID: "", Request: request, Target: target, SourceID: source, ManifestNode: manifestNode, ManifestRevision: manifestRevision,
		SelectedPaths: append([]string(nil), request.SelectedPaths...), Manifest: manifest, Items: items, ScanFingerprint: scan.fingerprint, ScopeID: scope, RequirementRoot: requirementRoot,
		DiscoverySnapshot: &discovery}
	plan.ID = materialPlanID(plan)
	return plan, nil
}

func materialsPlanIncludesPath(request domain.MaterialsSyncRequest, scan localMaterialScan, candidate string) bool {
	if len(request.SelectedPaths) == 0 || candidate == materialsRequirementRoot || candidate == materialsFilesRoot {
		return true
	}
	_, included := scan.files[candidate]
	return included
}

func sameNodeIdentity(node domain.FeishuWikiNode, entry domain.MaterialsSyncEntry) bool {
	return node.NodeToken == entry.NodeToken && node.ObjectToken == entry.ObjectToken && node.ObjectType == entry.ObjectType && node.ParentNodeToken == entry.ParentNodeToken && node.Title == entry.Title
}

func bindingEntry(binding *domain.MaterialsSyncBinding, path string) *domain.MaterialsSyncEntry {
	if binding == nil {
		return nil
	}
	entry, ok := binding.Entries[path]
	if !ok {
		return nil
	}
	return &entry
}

func materialsHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func materialPlanID(plan domain.MaterialsSyncPlan) string {
	plan.ID = ""
	// Hash a canonical snapshot without changing the review or execution order.
	plan.Items = append([]domain.MaterialsSyncItem(nil), plan.Items...)
	sort.SliceStable(plan.Items, func(i, j int) bool {
		left, right := plan.Items[i], plan.Items[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Action != right.Action {
			return left.Action < right.Action
		}
		return left.Kind < right.Kind
	})
	if plan.DiscoverySnapshot != nil {
		discovery := *plan.DiscoverySnapshot
		discovery.TargetChildren = append([]domain.FeishuWikiNode(nil), discovery.TargetChildren...)
		discovery.Managed = append([]domain.MaterialsSyncDiscoveryManifest(nil), discovery.Managed...)
		sort.Slice(discovery.TargetChildren, func(i, j int) bool {
			return discovery.TargetChildren[i].NodeToken < discovery.TargetChildren[j].NodeToken
		})
		sort.Slice(discovery.Managed, func(i, j int) bool { return discovery.Managed[i].Node.NodeToken < discovery.Managed[j].Node.NodeToken })
		plan.DiscoverySnapshot = &discovery
	}
	data, _ := json.Marshal(plan)
	return materialsHash(string(data))
}

func (s *Service) syncStateDirectory() string { return filepath.Join(s.paths.Cache, "materials-sync") }
func (s *Service) planPath(id string) (string, error) {
	if len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
		return "", errors.New("同步计划编号无效")
	}
	return filepath.Join(s.syncStateDirectory(), "plan-"+id+".json"), nil
}
func (s *Service) approvalPath(id string) (string, error) {
	path, err := s.planPath(id)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(path, ".json") + "-approval.json", nil
}

func (s *Service) saveMaterialsPlan(plan domain.MaterialsSyncPlan) error {
	path, err := s.planPath(plan.ID)
	if err != nil {
		return err
	}
	return writeAtomicJSON(path, plan)
}
func (s *Service) loadMaterialsPlan(id string) (domain.MaterialsSyncPlan, error) {
	path, err := s.planPath(id)
	if err != nil {
		return domain.MaterialsSyncPlan{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.MaterialsSyncPlan{}, err
	}
	var plan domain.MaterialsSyncPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return plan, fmt.Errorf("同步计划损坏：%w", err)
	}
	if !slices.Equal(plan.SelectedPaths, plan.Request.SelectedPaths) {
		return plan, errors.New("同步计划的资料范围记录不一致，请重新预览")
	}
	if plan.ID != id || materialPlanID(plan) != id {
		return plan, errors.New("同步计划摘要校验失败，请重新预览")
	}
	return plan, nil
}
func writeAtomicJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".sync-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (s *Service) confirmMaterials(ctx context.Context, config domain.AppConfig, payload map[string]any) (any, error) {
	id := stringValue(payload, "planId", "id")
	plan, err := s.loadMaterialsPlan(id)
	if err != nil {
		return nil, err
	}
	request, err := s.materialRequest(config, payloadWithRequest(payload, plan.Request))
	if err != nil {
		return nil, err
	}
	if !sameMaterialsSyncRequest(request, plan.Request) || !slices.Equal(plan.SelectedPaths, plan.Request.SelectedPaths) {
		return nil, errors.New("同步计划对应的本地路径或目标已变化，请重新预览")
	}
	deleteSet, overwriteSet := stringSet(stringSlice(payload, "deletionPaths")), stringSet(stringSlice(payload, "overwritePaths"))
	deletions, conflicts := map[string]bool{}, map[string]bool{}
	for _, item := range plan.Items {
		if item.Action == domain.MaterialsSyncDelete {
			deletions[item.Path] = true
		}
		if domain.MaterialsSyncItemCanOverwrite(item) {
			conflicts[item.Path] = true
		}
	}
	for path := range deleteSet {
		if !deletions[path] || path == materialsRequirementRoot || path == materialsFilesRoot {
			return nil, fmt.Errorf("删除确认项不属于本次计划：%s", path)
		}
	}
	for path := range overwriteSet {
		if !conflicts[path] || path == materialsRequirementRoot || path == materialsFilesRoot {
			return nil, fmt.Errorf("覆盖确认项不属于本次计划：%s", path)
		}
	}
	approval := domain.MaterialsSyncApproval{PlanID: id, DeletionPaths: toSet(deleteSet), OverwritePaths: toSet(overwriteSet)}
	if err := validateMaterialsApproval(plan, approval); err != nil {
		return nil, err
	}
	path, _ := s.approvalPath(id)
	if err := writeAtomicJSON(path, approval); err != nil {
		return nil, err
	}
	return map[string]any{"planId": id, "confirmedDeletions": stringSlice(payload, "deletionPaths"), "confirmedOverwrites": stringSlice(payload, "overwritePaths"), "ready": true}, nil
}

func payloadWithRequest(payload map[string]any, request domain.MaterialsSyncRequest) map[string]any {
	copy := make(map[string]any, len(payload)+6)
	for key, value := range payload {
		copy[key] = value
	}
	for key, value := range map[string]string{"targetUrl": request.TargetURL, "writeRoot": request.WriteRoot, "requirementId": request.RequirementID, "requirementTitle": request.RequirementTitle, "subdirectory": request.Subdirectory} {
		if _, exists := copy[key]; !exists {
			copy[key] = value
		}
	}
	if _, exists := copy["selectedPaths"]; !exists {
		copy["selectedPaths"] = append([]string(nil), request.SelectedPaths...)
	}
	return copy
}

func sameMaterialsSyncRequest(left, right domain.MaterialsSyncRequest) bool {
	return left.TargetURL == right.TargetURL && left.WriteRoot == right.WriteRoot && left.RequirementID == right.RequirementID &&
		left.RequirementTitle == right.RequirementTitle && left.Subdirectory == right.Subdirectory && slices.Equal(left.SelectedPaths, right.SelectedPaths)
}

func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		if value != "" {
			result[value] = true
		}
	}
	return result
}
func toSet(values map[string]bool) map[string]struct{} {
	result := map[string]struct{}{}
	for value := range values {
		result[value] = struct{}{}
	}
	return result
}

func (s *Service) materialsStatus(payload map[string]any) (any, error) {
	s.mu.Lock()
	last := s.lastSync
	active := make([]string, 0, len(s.active))
	for id := range s.active {
		active = append(active, id)
	}
	s.mu.Unlock()
	sort.Strings(active)
	state := "IDLE"
	if len(active) > 0 {
		state = "RUNNING"
	}
	if value, ok := last.(map[string]any); ok {
		result := make(map[string]any, len(value)+2)
		for key, item := range value {
			result[key] = item
		}
		result["active"] = active
		if state == "RUNNING" {
			result["state"] = state
		}
		return result, nil
	}
	return map[string]any{"state": state, "active": active, "last": last}, nil
}

func (s *Service) cancelMaterials(payload map[string]any) (any, error) {
	id := stringValue(payload, "planId", "operationId", "id")
	s.mu.Lock()
	cancel := s.active[id]
	s.mu.Unlock()
	if cancel == nil {
		return map[string]any{"cancelled": false, "message": "没有可取消的同步操作"}, nil
	}
	cancel()
	return map[string]any{"cancelled": true, "operationId": id}, nil
}

func (s *Service) recoverMaterials(ctx context.Context, config domain.AppConfig, payload map[string]any) (any, error) {
	if id := stringValue(payload, "planId", "id"); id != "" {
		previous, err := s.loadMaterialsPlan(id)
		if err != nil {
			return nil, err
		}
		current, err := s.buildMaterialsPlan(ctx, config, previous.Request)
		if err != nil {
			return nil, err
		}
		if err := s.saveMaterialsPlan(current); err != nil {
			return nil, err
		}
		pending := []domain.MaterialsSyncIntent{}
		if current.Manifest != nil {
			if binding, ok := current.Manifest.Bindings[current.SourceID]; ok {
				pending = append(pending, binding.Pending...)
			}
		}
		bootstrapPending := current.DiscoverySnapshot != nil && current.DiscoverySnapshot.Bootstrap != nil
		return map[string]any{"plan": current, "previousPlanId": previous.ID, "recoveryRequired": len(pending) > 0 || bootstrapPending,
			"pending": pending, "remoteReadOnly": true,
			"message": "已重新读取本地资料与飞书版本；请审核并确认返回的新计划后继续恢复。本步骤不写入远端。"}, nil
	}
	return s.planMaterials(ctx, config, payload)
}

func (s *Service) commentMaterials(ctx context.Context, config domain.AppConfig, payload map[string]any) (any, error) {
	selectedPaths, err := selectedMaterialsPaths(payload)
	if err != nil {
		return nil, err
	}
	if len(selectedPaths) > 0 {
		return nil, errors.New("文件级资料同步不更新整条需求评论")
	}
	content := stringValue(payload, "content")
	if content == "" {
		content, err = materialsCommentContent(ctx, s, config, payload)
		if err != nil {
			return nil, err
		}
		payload = clonePayload(payload)
		payload["content"] = content
		payload["confirmed"] = true
	}
	result, err := s.writeRequirementComment(ctx, config, payload)
	if value, ok := result.(domain.MaterialsCommentResult); ok && stringValue(payload, "directoryUrl") != "" {
		directoryURL := stringValue(payload, "directoryUrl")
		value.RequirementDirectoryURL = &directoryURL
		return value, err
	}
	return result, err
}

func materialsCommentContent(ctx context.Context, service *Service, config domain.AppConfig, payload map[string]any) (string, error) {
	request, err := service.materialRequest(config, payload)
	if err != nil {
		return "", err
	}
	directoryURL := stringValue(payload, "directoryUrl")
	validatedDirectory, err := domain.ValidateMaterialsFeishuTarget(directoryURL)
	if err != nil || validatedDirectory == nil {
		return "", errors.New("飞书需求资料目录地址无效")
	}
	targetURL, err := domain.ValidateMaterialsFeishuTarget(request.TargetURL)
	if err != nil || targetURL == nil {
		return "", errors.New("飞书同步目标地址无效")
	}
	target, _ := url.Parse(*targetURL)
	directory, _ := url.Parse(*validatedDirectory)
	if target == nil || directory == nil || !strings.EqualFold(target.Host, directory.Host) {
		return "", errors.New("飞书需求资料目录与同步目标不属于同一知识库域名")
	}
	item, err := service.requirementTarget(ctx, config, payload)
	if err != nil {
		return "", err
	}
	branches := materialsCommentBranches(payload["services"])
	serviceLines := "未关联服务"
	if len(branches) > 0 {
		lines := make([]string, 0, len(branches))
		seen := make(map[[2]string]struct{}, len(branches))
		for _, branch := range branches {
			name := strings.TrimSpace(branch.ServiceName)
			value := strings.TrimSpace(branch.Branch)
			if name == "" || value == "" {
				continue
			}
			key := [2]string{name, value}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			lines = append(lines, "- `"+materialsMarkdownCode(name)+"`：`"+materialsMarkdownCode(value)+"`")
		}
		if len(lines) > 0 {
			serviceLines = strings.Join(lines, "\n")
		}
	}
	marker := domain.MaterialsCommentMarker(item)
	absoluteRoot, err := filepath.Abs(request.WriteRoot)
	if err != nil {
		return "", err
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	return "SilverWing 需求资料\n\n飞书资料根目录：[" + *validatedDirectory + "](" + *validatedDirectory + ")\n本地需求资料目录：`" + materialsMarkdownCode(absoluteRoot) + "`\n\n服务与分支：\n" + serviceLines + "\n\n" + marker, nil
}

func materialsCommentBranches(value any) []domain.MaterialsServiceBranch {
	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 || string(data) == "null" {
		return nil
	}
	var rows []domain.MaterialsServiceBranch
	if json.Unmarshal(data, &rows) != nil {
		return nil
	}
	return rows
}

func materialsMarkdownCode(value string) string {
	return strings.NewReplacer("`", "'", "\r", " ", "\n", " ").Replace(value)
}

func clonePayload(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source)+2)
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func cloneMaterialsSyncProgress(progress domain.MaterialsSyncProgress) domain.MaterialsSyncProgress {
	progress.ActivePaths = append([]string{}, progress.ActivePaths...)
	progress.WaitingRetries = append([]domain.MaterialsSyncCommandRetry{}, progress.WaitingRetries...)
	return progress
}

func materialsWikiDirectoryURL(targetURL, nodeToken string) string {
	validated, err := domain.ValidateMaterialsFeishuTarget(targetURL)
	if err != nil || validated == nil || nodeToken == "" {
		return ""
	}
	parsed, err := url.Parse(*validated)
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.Path = "/wiki/" + nodeToken
	parsed.RawQuery, parsed.Fragment = "", ""
	return parsed.String()
}

func (s *Service) syncMaterials(ctx context.Context, config domain.AppConfig, payload map[string]any) (any, error) {
	execution, err := s.syncMaterialsWithProgress(ctx, config, payload, nil)
	return execution.Result, err
}

// SyncMaterialsWithProgress exposes the existing sync workflow to the desktop
// application without making the desktop depend on this integrations package.
func (s *Service) SyncMaterialsWithProgress(ctx context.Context, payload map[string]any, onProgress func(domain.MaterialsSyncProgress)) (MaterialsSyncExecution, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	config, err := s.config.Load()
	if err != nil {
		return MaterialsSyncExecution{}, err
	}
	return s.syncMaterialsWithProgress(ctx, config, payload, onProgress)
}

func (s *Service) syncMaterialsWithProgress(ctx context.Context, config domain.AppConfig, payload map[string]any, onProgress func(domain.MaterialsSyncProgress)) (MaterialsSyncExecution, error) {
	id := stringValue(payload, "planId", "id")
	plan, err := s.loadMaterialsPlan(id)
	if err != nil {
		return MaterialsSyncExecution{}, err
	}
	approvalPath, err := s.approvalPath(id)
	if err != nil {
		return MaterialsSyncExecution{}, err
	}
	approvalData, err := os.ReadFile(approvalPath)
	if err != nil {
		return MaterialsSyncExecution{}, errors.New("请先确认本次同步计划")
	}
	var approval domain.MaterialsSyncApproval
	if err := json.Unmarshal(approvalData, &approval); err != nil || approval.PlanID != id {
		return MaterialsSyncExecution{}, errors.New("同步确认记录无效，请重新确认")
	}
	operationCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if _, exists := s.active[id]; exists {
		s.mu.Unlock()
		cancel()
		return MaterialsSyncExecution{}, errors.New("该同步计划正在执行")
	}
	s.active[id] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.active, id)
		s.mu.Unlock()
	}()
	result, directoryURL, err := s.applyMaterialsPlanWithProgress(operationCtx, config, plan, approval, onProgress)
	if err != nil && materialsSyncCancellation(err, operationCtx.Err()) {
		result.Cancelled = true
	} else if err != nil && result.Progress.Failed == 0 {
		result.Progress.Failed = 1
	}
	if onProgress != nil {
		onProgress(cloneMaterialsSyncProgress(result.Progress))
	}
	s.mu.Lock()
	if err != nil {
		state := "FAILED"
		if result.Cancelled {
			state = "CANCELLED"
		}
		s.lastSync = map[string]any{"operationId": id, "state": state, "error": s.safeDiagnostic(config, err.Error()), "result": result}
	} else {
		s.lastSync = result
	}
	s.mu.Unlock()
	return MaterialsSyncExecution{Result: result, RequirementDirectoryURL: directoryURL}, err
}

func materialsSyncCancellation(operationErr error, cancellationCauses ...error) bool {
	if process.IsCleanupFailure(operationErr) {
		return false
	}
	for _, cause := range cancellationCauses {
		if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
			return true
		}
	}
	return false
}

func (s *Service) applyMaterialsPlan(ctx context.Context, config domain.AppConfig, plan domain.MaterialsSyncPlan, approval domain.MaterialsSyncApproval) (domain.MaterialsSyncResult, error) {
	result, _, err := s.applyMaterialsPlanWithProgress(ctx, config, plan, approval, nil)
	return result, err
}

func (s *Service) applyMaterialsPlanWithProgress(ctx context.Context, config domain.AppConfig, plan domain.MaterialsSyncPlan, approval domain.MaterialsSyncApproval, onProgress func(domain.MaterialsSyncProgress)) (domain.MaterialsSyncResult, string, error) {
	if !slices.Equal(plan.SelectedPaths, plan.Request.SelectedPaths) {
		return domain.MaterialsSyncResult{}, "", errors.New("同步计划的资料范围记录不一致，请重新预览")
	}
	scan, err := scanMaterialsForRequest(plan.Request.WriteRoot, plan.Request.SelectedPaths)
	if err != nil {
		return domain.MaterialsSyncResult{}, "", err
	}
	if scan.fingerprint != plan.ScanFingerprint {
		return domain.MaterialsSyncResult{}, "", errors.New("本地资料已改变，请重新预览")
	}
	if plan.ID != materialPlanID(plan) || approval.PlanID != plan.ID {
		return domain.MaterialsSyncResult{}, "", errors.New("同步计划或确认范围已改变，请重新预览")
	}
	if err := validateMaterialsApproval(plan, approval); err != nil {
		return domain.MaterialsSyncResult{}, "", err
	}
	// Changes are checkpointed to the remote manifest after every resource receipt.
	// No write is attempted from preview or confirm; only this explicit sync action writes.
	totalFiles := 0
	for _, file := range scan.files {
		if file.kind != domain.MaterialsSyncDirectory {
			totalFiles++
		}
	}
	result := domain.MaterialsSyncResult{Progress: domain.MaterialsSyncProgress{Phase: domain.MaterialsSyncInitialize, TotalFiles: totalFiles}}
	if onProgress != nil {
		onProgress(cloneMaterialsSyncProgress(result.Progress))
	}
	directoryURL := ""
	err = s.applyMaterialsRemoteWithProgress(ctx, config, plan, approval, scan, &result, onProgress, func(value string) { directoryURL = value })
	return result, directoryURL, err
}

func scanMaterials(rawRoot string) (localMaterialScan, error) {
	return scanMaterialsForRequest(rawRoot, nil)
}

func scanMaterialsForRequest(rawRoot string, selectedPaths []string) (localMaterialScan, error) {
	root, err := filepath.Abs(rawRoot)
	if err != nil {
		return localMaterialScan{}, err
	}
	root = filepath.Clean(root)
	info, err := os.Lstat(root)
	if err != nil {
		return localMaterialScan{}, fmt.Errorf("任务资料目录不可用：%w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return localMaterialScan{}, errors.New("任务资料目录必须是普通目录")
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil || filepath.Clean(realRoot) != root {
		return localMaterialScan{}, errors.New("任务资料路径包含符号链接或重解析点")
	}
	files, blocked := map[string]localMaterial{}, []string{}
	citationMarkdown := map[string]bool{}
	var visit func(string) error
	visit = func(directory string) error {
		before, err := os.Stat(directory)
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if excludedMaterialsName(entry.Name()) {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			real, evalErr := filepath.EvalSymlinks(path)
			if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeIrregular != 0 || evalErr != nil || filepath.Clean(real) != filepath.Clean(path) {
				blocked = append(blocked, relative)
				continue
			}
			if info.IsDir() {
				files[relative] = localMaterial{path: relative, kind: domain.MaterialsSyncDirectory, hash: "directory"}
				if err := visit(path); err != nil {
					return err
				}
			} else if info.Mode().IsRegular() {
				kind := domain.MaterialsSyncFile
				if strings.EqualFold(filepath.Ext(entry.Name()), ".md") || strings.EqualFold(filepath.Ext(entry.Name()), ".markdown") {
					kind = domain.MaterialsSyncMarkdown
				}
				hash, err := hashLocalFile(path)
				if err != nil {
					return fmt.Errorf("读取资料失败 %s：%w", relative, err)
				}
				files[relative] = localMaterial{path: relative, kind: kind, hash: hash}
			} else {
				return fmt.Errorf("无法读取特殊文件：%s", relative)
			}
		}
		after, err := os.Stat(directory)
		if err != nil || before.ModTime() != after.ModTime() {
			return errors.New("扫描时资料目录发生变化，请重试")
		}
		return nil
	}
	if err := visit(root); err != nil {
		return localMaterialScan{}, err
	}
	allFiles := make(map[string]localMaterial, len(files))
	for path, file := range files {
		allFiles[path] = file
	}
	if len(selectedPaths) > 0 {
		files, blocked, citationMarkdown, err = selectedMaterialsFiles(root, files, blocked, selectedPaths)
		if err != nil {
			return localMaterialScan{}, err
		}
	}
	// Dependencies always refer to original file hashes, not another Markdown
	// file's already-computed dependency hash from this map iteration.
	for path, file := range files {
		if file.kind != domain.MaterialsSyncMarkdown {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return localMaterialScan{}, err
		}
		dependencies, linked, err := markdownDependencies(root, path, string(content), allFiles)
		if err != nil {
			return localMaterialScan{}, err
		}
		includedDependencies := make([]string, 0, len(dependencies))
		for _, dependency := range dependencies {
			pathEnd := strings.LastIndexByte(dependency, ':')
			if pathEnd > 0 {
				if _, included := files[dependency[:pathEnd]]; included {
					includedDependencies = append(includedDependencies, dependency)
				}
			}
		}
		includedLinks := make([]string, 0, len(linked))
		for _, linkedPath := range linked {
			if _, included := files[linkedPath]; included {
				includedLinks = append(includedLinks, linkedPath)
			}
		}
		file.hash = materialsHash(file.hash + "\n" + strings.Join(includedDependencies, "\n"))
		file.linkedPaths = includedLinks
		files[path] = file
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	parts := make([]string, 0, len(paths)+1)
	for _, path := range paths {
		file := files[path]
		parts = append(parts, path+":"+string(file.kind)+":"+file.hash)
	}
	sort.Strings(blocked)
	parts = append(parts, "blocked:"+strings.Join(blocked, ","))
	return localMaterialScan{root: root, files: files, allFiles: allFiles, fingerprint: materialsHash(strings.Join(parts, "\n")), blocked: blocked, citationMarkdown: citationMarkdown}, nil
}

func selectedMaterialsFiles(root string, files map[string]localMaterial, blocked []string, selectedPaths []string) (map[string]localMaterial, []string, map[string]bool, error) {
	selected := make(map[string]localMaterial)
	citationMarkdown := make(map[string]bool)
	explicit := make(map[string]bool, len(selectedPaths))
	for _, requested := range selectedPaths {
		relative, err := validateMaterialsRelativePath(requested)
		if err != nil {
			return nil, nil, nil, err
		}
		explicit[relative] = true
	}
	queued := []string{}
	queuedDocs := map[string]bool{}
	add := func(relative string) error {
		file, ok := files[relative]
		if !ok {
			return fmt.Errorf("资料不存在或包含符号链接，无法按文件范围同步：%s", relative)
		}
		selected[relative] = file
		if file.kind == domain.MaterialsSyncMarkdown && !queuedDocs[relative] {
			queuedDocs[relative] = true
			queued = append(queued, relative)
		}
		return addMaterialAncestors(relative, files, selected)
	}
	addIndexes := func(relative string) error {
		directory := path.Dir(relative)
		for {
			for candidate, file := range files {
				if path.Dir(candidate) == directory && strings.EqualFold(path.Base(candidate), "Index.md") && file.kind == domain.MaterialsSyncMarkdown {
					if !explicit[candidate] {
						citationMarkdown[candidate] = true
					}
					if err := add(candidate); err != nil {
						return err
					}
				}
			}
			if directory == "." {
				break
			}
			directory = path.Dir(directory)
		}
		return nil
	}
	for _, requested := range selectedPaths {
		relative, err := validateMaterialsRelativePath(requested)
		if err != nil {
			return nil, nil, nil, err
		}
		file, ok := files[relative]
		if !ok {
			for _, blockedPath := range blocked {
				if relative == blockedPath || strings.HasPrefix(relative, blockedPath+"/") {
					return nil, nil, nil, fmt.Errorf("资料路径包含符号链接或特殊文件：%s", blockedPath)
				}
			}
			return nil, nil, nil, fmt.Errorf("所选资料不是可同步的文件：%s", relative)
		}
		if file.kind == domain.MaterialsSyncDirectory {
			return nil, nil, nil, fmt.Errorf("只能按文件范围同步：%s", relative)
		}
		if err := add(relative); err != nil {
			return nil, nil, nil, err
		}
		if err := addIndexes(relative); err != nil {
			return nil, nil, nil, err
		}
	}
	for len(queued) > 0 {
		document := queued[0]
		queued = queued[1:]
		content, err := readMarkdownLocal(root, document)
		if err != nil {
			return nil, nil, nil, err
		}
		dependencies, _, err := markdownDependencies(root, document, content, files)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, dependency := range dependencies {
			pathEnd := strings.LastIndexByte(dependency, ':')
			if pathEnd <= 0 {
				return nil, nil, nil, errors.New("Markdown 资源依赖路径无效")
			}
			dependencyPath := dependency[:pathEnd]
			// Linked Markdown documents are independent materials. In particular,
			// parent Index.md files commonly link every sibling document; uploading
			// one selected file must not expand into those unselected siblings. The
			// rewrite step reuses an existing mapping, leaves an unmapped parent-index
			// link as a text citation, and fails closed for an explicitly selected doc.
			if files[dependencyPath].kind == domain.MaterialsSyncMarkdown {
				continue
			}
			if err := add(dependencyPath); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	return selected, []string{}, citationMarkdown, nil
}

func addMaterialAncestors(relative string, files, selected map[string]localMaterial) error {
	directory := path.Dir(relative)
	for directory != "." {
		parent, ok := files[directory]
		if !ok || parent.kind != domain.MaterialsSyncDirectory {
			return fmt.Errorf("所选资料的父目录不存在或不安全：%s", directory)
		}
		selected[directory] = parent
		directory = path.Dir(directory)
	}
	return nil
}

func excludedMaterialsName(name string) bool {
	lower := strings.ToLower(name)
	if lower == ".git" || lower == ".silverwing-requirement.json" || lower == ".silverwing-requirement-index.jsonl" || lower == ".silverwing-iteration.json" || lower == ".silverwing-requirement-materials.lock" {
		return true
	}
	return strings.HasPrefix(name, "~$") || strings.HasSuffix(name, "~") || regexp.MustCompile(`(?i)\.(tmp|temp|swp|swo)$`).MatchString(name) || strings.HasPrefix(lower, ".silverwing-paste-") || strings.HasPrefix(lower, ".silverwing-rename-")
}

func hashLocalFile(path string) (string, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("资料不是普通文件")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	after, err := os.Lstat(path)
	if err != nil || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return "", errors.New("读取期间文件发生变化")
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func markdownDependencies(root, sourcePath, content string, files map[string]localMaterial) ([]string, []string, error) {
	dependencies, linked := map[string]string{}, map[string]bool{}
	_, err := rewriteMaterialsMarkdownResources(root, sourcePath, content, func(path string, image bool) (string, error) {
		dependency, ok := files[path]
		if !ok {
			return "", fmt.Errorf("Markdown 引用了不存在或不能上传的资料：%s（来源：%s）", path, sourcePath)
		}
		if image && dependency.kind != domain.MaterialsSyncFile {
			return "", fmt.Errorf("图片引用必须指向普通图片文件：%s", path)
		}
		dependencies[path] = dependency.hash
		if !image {
			linked[path] = true
		}
		return path, nil
	})
	if err != nil {
		return nil, nil, err
	}
	paths := sortedKeys(dependencies)
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		result = append(result, path+":"+dependencies[path])
	}
	links := sortedKeys(linked)
	return result, links, nil
}

func rewriteRemoteMarkdown(scan localMaterialScan, sourcePath, content string, binding domain.MaterialsSyncBinding, targetURL string) (string, error) {
	return rewriteMaterialsMarkdownResources(scan.root, sourcePath, content, func(relative string, image bool) (string, error) {
		dependency, exists := scan.files[relative]
		if image {
			if !exists || dependency.kind != domain.MaterialsSyncFile {
				return "", fmt.Errorf("Markdown 引用了不存在或不能上传的资料：%s", relative)
			}
			return "<@./" + relative + ">", nil
		}
		entry, mapped := binding.Entries[relative]
		if !exists && !isMaterialsMarkdownPath(relative) {
			return "", fmt.Errorf("Markdown 引用了不存在或不能上传的资料：%s", relative)
		}
		if mapped {
			if entry.Kind != domain.MaterialsSyncMarkdown && entry.Kind != domain.MaterialsSyncFile || entry.Kind == domain.MaterialsSyncMarkdown && entry.ObjectType != "docx" || entry.Kind == domain.MaterialsSyncFile && entry.ObjectType != "file" {
				return "", fmt.Errorf("Markdown 引用的远端资料类型无效：%s", relative)
			}
		} else {
			if !scan.citationMarkdown[sourcePath] || exists && dependency.kind != domain.MaterialsSyncMarkdown || !exists && !isMaterialsMarkdownPath(relative) {
				return "", fmt.Errorf("Markdown 引用的远端资料尚未登记：%s", relative)
			}
			// Automatically included parent Index.md files can remain useful before
			// every sibling Markdown document is uploaded. The shared walker turns
			// this unmapped sibling link into a readable text citation.
			return "", errMaterialsTextCitation
		}
		parsedTarget, err := url.Parse(targetURL)
		if err != nil || parsedTarget.Host == "" || parsedTarget.Scheme != "https" {
			return "", errors.New("飞书同步目标 URL 无效")
		}
		return parsedTarget.Scheme + "://" + parsedTarget.Host + "/wiki/" + entry.NodeToken, nil
	})
}

func isMaterialsMarkdownPath(relative string) bool {
	extension := path.Ext(relative)
	return strings.EqualFold(extension, ".md") || strings.EqualFold(extension, ".markdown")
}

func (s *Service) applyMaterialsRemote(ctx context.Context, config domain.AppConfig, plan domain.MaterialsSyncPlan, approval domain.MaterialsSyncApproval, scan localMaterialScan, result *domain.MaterialsSyncResult) error {
	return s.applyMaterialsRemoteWithProgress(ctx, config, plan, approval, scan, result, nil, nil)
}

func (s *Service) applyMaterialsRemoteWithProgress(ctx context.Context, config domain.AppConfig, plan domain.MaterialsSyncPlan, approval domain.MaterialsSyncApproval, scan localMaterialScan, result *domain.MaterialsSyncResult, onProgress func(domain.MaterialsSyncProgress), onDirectoryURL func(string)) (syncFailure error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	release, err := s.lockMaterialsScope(plan.ScopeID)
	if err != nil {
		return err
	}
	defer func() {
		finishMaterialsScopeLock(filepath.Join(s.syncStateDirectory(), plan.ScopeID), release, syncFailure)
	}()
	notify := func() {
		if onProgress != nil {
			onProgress(cloneMaterialsSyncProgress(result.Progress))
		}
	}
	current, err := s.buildMaterialsPlan(ctx, config, plan.Request)
	if err != nil {
		return err
	}
	if current.ID != plan.ID {
		return errors.New("飞书目标、远端版本或本地资料在确认后已改变，请重新预览")
	}
	gateway := newFeishuGateway(ctx, s, config)
	journalPath := filepath.Join(s.syncStateDirectory(), "journal-"+plan.ScopeID+".json")
	if err := writeAtomicJSON(journalPath, map[string]any{"planId": plan.ID, "scopeId": plan.ScopeID, "phase": "INITIALIZE", "startedAt": time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		return err
	}
	state, err := s.openOrBootstrapMaterials(ctx, gateway, plan)
	if err != nil {
		_ = writeAtomicJSON(journalPath, map[string]any{"planId": plan.ID, "scopeId": plan.ScopeID, "phase": "FAILED", "error": s.safeDiagnostic(config, err.Error())})
		return err
	}
	if onDirectoryURL != nil {
		onDirectoryURL(materialsWikiDirectoryURL(plan.Request.TargetURL, state.root.NodeToken))
	}
	state, recovered, err := gateway.recoverMaterialsReceipts(state, plan, scan, approval)
	if err == nil {
		var intentRecovered []string
		state, intentRecovered, err = gateway.recoverPending(state, plan, scan, approval)
		recovered = append(recovered, intentRecovered...)
	}
	if err != nil {
		_ = writeAtomicJSON(journalPath, map[string]any{"planId": plan.ID, "scopeId": plan.ScopeID, "phase": "RECOVERY_REQUIRED", "error": s.safeDiagnostic(config, err.Error())})
		return err
	}
	if len(recovered) > 0 {
		refreshed, err := s.buildMaterialsPlan(ctx, config, plan.Request)
		if err != nil {
			return err
		}
		if refreshed.ScanFingerprint != plan.ScanFingerprint || refreshed.ScopeID != plan.ScopeID || refreshed.SourceID != plan.SourceID {
			return errors.New("恢复期间本地资料或飞书目标发生变化，请重新预览")
		}
		if err := validateRecoveredApproval(plan, refreshed, approval); err != nil {
			return err
		}
		plan = refreshed
	}
	binding, exists := state.value.Bindings[plan.SourceID]
	if !exists {
		var rootEntry domain.MaterialsSyncEntry
		found := false
		for _, candidate := range state.value.Bindings {
			if entry, ok := candidate.Entries[materialsRequirementRoot]; ok {
				rootEntry, found = entry, true
				break
			}
		}
		if !found {
			return errors.New("飞书同步配置缺少受保护的需求根登记")
		}
		binding = domain.MaterialsSyncBinding{SourceID: plan.SourceID, RequirementID: plan.Request.RequirementID, RequirementTitle: plan.Request.RequirementTitle, Subdirectory: plan.Request.Subdirectory, Entries: map[string]domain.MaterialsSyncEntry{materialsRequirementRoot: rootEntry}, Pending: []domain.MaterialsSyncIntent{}}
		state.value.Bindings[plan.SourceID] = binding
		state, err = gateway.saveManifest(state, plan.Target, plan.Request.WriteRoot, state.value)
		if err != nil {
			return err
		}
	}
	binding = state.value.Bindings[plan.SourceID]
	if binding.Entries == nil {
		binding.Entries = map[string]domain.MaterialsSyncEntry{}
		state.value.Bindings[plan.SourceID] = binding
	}
	state, err = s.applyMaterialsResourceWrites(ctx, gateway, config, plan, approval, scan, state, result, notify)
	if err != nil {
		phase := "PARTIAL_FAILURE"
		if materialsSyncCancellation(err, ctx.Err(), err) {
			phase = "CANCELLED"
		}
		if journalErr := writeAtomicJSON(journalPath, map[string]any{"planId": plan.ID, "scopeId": plan.ScopeID, "phase": phase,
			"error": s.safeDiagnostic(config, err.Error()), "result": result}); journalErr != nil {
			message := s.safeDiagnostic(config, journalErr.Error())
			result.Messages = append(result.Messages, "本地同步恢复记录写入失败："+message)
			err = errors.Join(err, fmt.Errorf("本地同步恢复记录写入失败：%s", message))
		}
		return err
	}
	result.Progress.Phase = domain.MaterialsSyncComplete
	result.Progress.Conflicts = countPlanActions(plan, domain.MaterialsSyncConflict) - len(approval.OverwritePaths)
	result.Messages = append(result.Messages, "同步完成；远端同步清单已逐项校验并保存。")
	notify()
	if err := writeAtomicJSON(journalPath, map[string]any{"planId": plan.ID, "scopeId": plan.ScopeID, "phase": "COMPLETE", "result": result}); err != nil {
		return err
	}
	return nil
}

type remoteMaterialsState struct {
	control  domain.FeishuWikiNode
	revision int64
	value    domain.MaterialsSyncManifest
	root     domain.FeishuWikiNode
}

func (g *feishuGateway) saveManifest(state remoteMaterialsState, target domain.FeishuWikiNode, writeRoot string, value domain.MaterialsSyncManifest) (remoteMaterialsState, error) {
	if err := validateRemoteManifest(value, target, state.root, state.control); err != nil {
		return state, materialsManifestStateFailure(err)
	}
	written, err := g.OverwriteDocument(state.control, encodeMaterialsManifest(value), state.revision, writeRoot, nil, &state.root)
	if err != nil {
		return state, materialsManifestStateFailure(err)
	}
	doc, err := g.ReadDocument(written.Node, false)
	if err != nil {
		return state, materialsManifestStateFailure(err)
	}
	saved, err := decodeMaterialsManifest(doc.Content)
	if err != nil || !equalManifest(saved, value) || doc.Revision <= state.revision {
		return state, materialsManifestStateFailure(errors.New("飞书同步配置写入后回读不一致，已停止同步"))
	}
	state.control, state.revision, state.value = written.Node, doc.Revision, saved
	return state, nil
}

func equalManifest(a, b domain.MaterialsSyncManifest) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func (g *feishuGateway) persistIntent(state remoteMaterialsState, plan domain.MaterialsSyncPlan, intent domain.MaterialsSyncIntent, allowSupersedeIntent ...bool) (remoteMaterialsState, error) {
	value := state.value
	binding := value.Bindings[plan.SourceID]
	if binding.SourceID == "" {
		return state, errors.New("同步绑定不存在")
	}
	for index, previous := range binding.Pending {
		createPhaseTransition := previous.OperationID == intent.OperationID &&
			(previous.Phase == "create" || previous.Phase == "create-attempted") &&
			(intent.Phase == previous.Phase || previous.Phase == "create" && intent.Phase == "create-attempted")
		if previous.Path == intent.Path && (previous.Phase == intent.Phase || createPhaseTransition) {
			if previous.OperationID == intent.OperationID && sameMaterialsIntent(previous, intent) {
				return state, nil
			}
			if previous.OperationID == intent.OperationID && (previous.Phase == "create" || previous.Phase == "create-attempted") &&
				(intent.Phase == previous.Phase || previous.Phase == "create" && intent.Phase == "create-attempted") {
				previousCore, intentCore := previous, intent
				previousCore.Phase, intentCore.Phase = "create", "create"
				previousCore.NodeToken, intentCore.NodeToken = nil, nil
				if sameMaterialsIntent(previousCore, intentCore) {
					binding.Pending[index] = intent
					value.Bindings[plan.SourceID] = binding
					value.Generation++
					return g.saveManifest(state, plan.Target, plan.Request.WriteRoot, value)
				}
			}
			allowSupersede := len(allowSupersedeIntent) > 0 && allowSupersedeIntent[0]
			baselineUnchanged := previous.BaseVersion != nil && intent.BaseVersion != nil && *previous.BaseVersion == *intent.BaseVersion
			identityUnchanged := previous.Kind == intent.Kind && previous.NodeToken != nil && intent.NodeToken != nil &&
				*previous.NodeToken == *intent.NodeToken && previous.ParentNodeToken == intent.ParentNodeToken && previous.Title == intent.Title
			if intent.Phase != "update" || !identityUnchanged || (!baselineUnchanged && !allowSupersede) {
				return state, fmt.Errorf("已有未确认的远端写入操作 %s，不能安全替换；请重新检查远端版本", intent.Path)
			}
			binding.Pending = append(binding.Pending[:index], binding.Pending[index+1:]...)
			break
		}
	}
	binding.Pending = append(binding.Pending, intent)
	value.Bindings[plan.SourceID] = binding
	value.Generation++
	return g.saveManifest(state, plan.Target, plan.Request.WriteRoot, value)
}

func sameMaterialsIntent(a, b domain.MaterialsSyncIntent) bool {
	if a.OperationID != b.OperationID || a.Path != b.Path || a.Kind != b.Kind || a.ParentNodeToken != b.ParentNodeToken ||
		a.Title != b.Title || a.TemporaryTitle != b.TemporaryTitle || a.LocalHash != b.LocalHash || a.Phase != b.Phase ||
		a.Placeholder != b.Placeholder || a.ExpectedContentHash == nil != (b.ExpectedContentHash == nil) || a.BaseVersion == nil != (b.BaseVersion == nil) || a.NodeToken == nil != (b.NodeToken == nil) {
		return false
	}
	if a.ExpectedContentHash != nil && *a.ExpectedContentHash != *b.ExpectedContentHash {
		return false
	}
	if a.BaseVersion != nil && *a.BaseVersion != *b.BaseVersion {
		return false
	}
	if a.NodeToken != nil && *a.NodeToken != *b.NodeToken {
		return false
	}
	return true
}

func createIntentFor(binding domain.MaterialsSyncBinding, path string, kind domain.MaterialsSyncKind, parentToken, title, localHash string, placeholder bool) (domain.MaterialsSyncIntent, error) {
	for _, pending := range binding.Pending {
		if pending.Path != path {
			continue
		}
		if pending.Phase != "create" && pending.Phase != "create-attempted" || pending.Kind != kind || pending.ParentNodeToken != parentToken || pending.Title != title || pending.LocalHash != localHash || pending.Placeholder != placeholder {
			return domain.MaterialsSyncIntent{}, fmt.Errorf("路径 %s 存在内容或父目录不匹配的待恢复操作", path)
		}
		return pending, nil
	}
	id, err := newIntegrationUUID()
	if err != nil {
		return domain.MaterialsSyncIntent{}, err
	}
	return domain.MaterialsSyncIntent{OperationID: id, Path: path, Kind: kind, ParentNodeToken: parentToken, Title: title,
		TemporaryTitle: title + " [sw-" + id + "]", LocalHash: localHash, Phase: "create", Placeholder: placeholder}, nil
}

func validateRecoveredApproval(previous, refreshed domain.MaterialsSyncPlan, approval domain.MaterialsSyncApproval) error {
	if previous.ScopeID != refreshed.ScopeID || previous.SourceID != refreshed.SourceID || previous.ScanFingerprint != refreshed.ScanFingerprint ||
		!sameMaterialsSyncRequest(previous.Request, refreshed.Request) || !slices.Equal(previous.SelectedPaths, refreshed.SelectedPaths) || previous.Target != refreshed.Target {
		return errors.New("恢复后的计划目标或本地资料与已确认计划不一致")
	}
	find := func(plan domain.MaterialsSyncPlan, path string, action domain.MaterialsSyncAction, requireLocal bool) (domain.MaterialsSyncItem, bool) {
		for _, item := range plan.Items {
			if item.Path == path && item.Action == action && (!requireLocal || item.LocalHash != "") &&
				(action != domain.MaterialsSyncConflict || !requireLocal || domain.MaterialsSyncItemCanOverwrite(item)) {
				return item, true
			}
		}
		return domain.MaterialsSyncItem{}, false
	}
	for path := range approval.DeletionPaths {
		old, oldOK := find(previous, path, domain.MaterialsSyncDelete, false)
		if !oldOK || old.Action != domain.MaterialsSyncDelete || old.Entry == nil {
			return fmt.Errorf("已确认删除路径与原计划不一致：%s", path)
		}
		current, ok := find(refreshed, path, domain.MaterialsSyncDelete, false)
		if !ok { // The pending, previously approved delete was completed during recovery.
			continue
		}
		if current.Action != domain.MaterialsSyncDelete || current.Entry == nil || *current.Entry != *old.Entry ||
			!sameObservedSnapshot(old, current) {
			return fmt.Errorf("恢复后的删除对象或版本已改变：%s", path)
		}
	}
	for path := range approval.OverwritePaths {
		old, oldOK := find(previous, path, domain.MaterialsSyncConflict, true)
		if !oldOK || old.Action != domain.MaterialsSyncConflict || old.Entry == nil || old.LocalHash == "" {
			return fmt.Errorf("已确认覆盖路径与原计划不一致：%s", path)
		}
		current, ok := find(refreshed, path, domain.MaterialsSyncConflict, true)
		if !ok {
			current, ok = find(refreshed, path, domain.MaterialsSyncSkip, true)
		}
		if !ok {
			current, ok = find(refreshed, path, domain.MaterialsSyncUpdate, true)
		}
		if !ok || current.Entry == nil || current.Entry.NodeToken != old.Entry.NodeToken || current.Entry.ObjectToken != old.Entry.ObjectToken || current.LocalHash != old.LocalHash {
			return fmt.Errorf("恢复后的覆盖对象与本地内容已改变：%s", path)
		}
		if current.Action == domain.MaterialsSyncSkip || current.Action == domain.MaterialsSyncUpdate {
			continue // Recovery proved the previously authorized write completed.
		}
		if current.Action != domain.MaterialsSyncConflict || !sameObservedSnapshot(old, current) {
			return fmt.Errorf("恢复后的覆盖对象或版本已改变：%s", path)
		}
	}
	return nil
}

func sameObservedSnapshot(a, b domain.MaterialsSyncItem) bool {
	if a.ObservedVersion == nil != (b.ObservedVersion == nil) || a.ObservedNode == nil != (b.ObservedNode == nil) {
		return false
	}
	if a.ObservedVersion != nil && *a.ObservedVersion != *b.ObservedVersion {
		return false
	}
	return a.ObservedNode == nil || *a.ObservedNode == *b.ObservedNode
}

// recoverPending only reconciles operations that were durably recorded before the
// corresponding remote write. It adopts a result when its identity and content
// prove that the recorded operation completed; otherwise it leaves the entry for
// a freshly reviewed plan or fails closed.
func (g *feishuGateway) recoverPending(state remoteMaterialsState, plan domain.MaterialsSyncPlan, scan localMaterialScan, approval domain.MaterialsSyncApproval) (remoteMaterialsState, []string, error) {
	binding, ok := state.value.Bindings[plan.SourceID]
	if !ok || len(binding.Pending) == 0 {
		return state, nil, nil
	}
	recovered := []string{}
	pending := append([]domain.MaterialsSyncIntent(nil), binding.Pending...)
	for _, intent := range pending {
		if !materialsPlanIncludesPath(plan.Request, scan, intent.Path) {
			continue
		}
		// Creation recovery belongs to the same depth wave as creation.
		if intent.Phase == "create" || intent.Phase == "create-attempted" {
			continue
		}
		if !validRemoteMaterialsPath(intent.Path) || intent.OperationID == "" || intent.ParentNodeToken == "" || intent.Title == "" {
			return state, recovered, errors.New("飞书同步清单包含不完整的待恢复操作")
		}
		switch intent.Phase {
		case "delete":
			if intent.NodeToken == nil || intent.BaseVersion == nil {
				return state, recovered, fmt.Errorf("待删除操作缺少节点或版本基线：%s", intent.Path)
			}
			entry, exists := binding.Entries[intent.Path]
			if !exists || entry.NodeToken != *intent.NodeToken || entry.Kind != intent.Kind {
				return state, recovered, fmt.Errorf("待删除操作与登记资源不一致：%s", intent.Path)
			}
			remote, err := g.GetNode(*intent.NodeToken)
			if err != nil {
				return state, recovered, err
			}
			if remote == nil {
				state, err = g.removeRecoveredEntry(state, plan, intent)
				if err != nil {
					return state, recovered, err
				}
				recovered = append(recovered, intent.Path)
				binding = state.value.Bindings[plan.SourceID]
				continue
			}
			if !sameNodeIdentity(*remote, entry) {
				return state, recovered, fmt.Errorf("待删除远端节点身份已改变：%s", intent.Path)
			}
			version, err := g.Version(*remote, materialsIncludeComments(intent.Path))
			if err != nil {
				return state, recovered, err
			}
			if version != *intent.BaseVersion {
				return state, recovered, fmt.Errorf("待删除远端版本已改变，需重新预览：%s", intent.Path)
			}
			if _, confirmed := approval.DeletionPaths[intent.Path]; !confirmed {
				continue
			}
			if err := g.DeleteNode(*remote); err != nil {
				return state, recovered, err
			}
			state, err = g.removeRecoveredEntry(state, plan, intent)
			if err != nil {
				return state, recovered, err
			}
			recovered = append(recovered, intent.Path)
			binding = state.value.Bindings[plan.SourceID]
		case "update":
			entry, exists := binding.Entries[intent.Path]
			if !exists || intent.NodeToken == nil || intent.BaseVersion == nil || entry.NodeToken != *intent.NodeToken || entry.Kind != intent.Kind || entry.Title != intent.Title || entry.ParentNodeToken != intent.ParentNodeToken {
				return state, recovered, fmt.Errorf("待更新操作与登记资源不一致：%s", intent.Path)
			}
			file, exists := scan.files[intent.Path]
			if !exists || file.hash != intent.LocalHash || file.kind != intent.Kind {
				return state, recovered, fmt.Errorf("待更新本地内容已改变：%s", intent.Path)
			}
			remote, err := g.GetNode(entry.NodeToken)
			if err != nil {
				return state, recovered, err
			}
			if remote == nil || !sameNodeIdentity(*remote, entry) {
				return state, recovered, fmt.Errorf("待更新远端节点身份已改变：%s", intent.Path)
			}
			version, err := g.Version(*remote, true)
			if err != nil {
				return state, recovered, err
			}
			if version == *intent.BaseVersion {
				continue // The previous request did not take effect; normal sync may retry it.
			}
			completed := false
			if intent.Kind == domain.MaterialsSyncMarkdown && intent.ExpectedContentHash != nil && version.CommentsFingerprint == intent.BaseVersion.CommentsFingerprint {
				doc, readErr := g.ReadDocument(*remote, false)
				if process.IsCleanupFailure(readErr) {
					return state, recovered, readErr
				}
				completed = readErr == nil && fmt.Sprint(doc.Revision) == version.Version && materialsContentHash(doc.Content) == *intent.ExpectedContentHash
			} else if intent.Kind == domain.MaterialsSyncFile && version.CommentsFingerprint == intent.BaseVersion.CommentsFingerprint {
				fingerprint, readErr := g.ReadFileFingerprint(*remote)
				if process.IsCleanupFailure(readErr) {
					return state, recovered, readErr
				}
				completed = readErr == nil && fingerprint.SHA256 == intent.LocalHash && fingerprint.RemoteVersion == version
			}
			if completed {
				entry.LocalHash, entry.RemoteVersion = intent.LocalHash, version
				binding.Entries[intent.Path] = entry
				binding.Pending = removeMaterialIntent(binding.Pending, intent.OperationID)
				state.value.Bindings[plan.SourceID] = binding
				state.value.Generation++
				state, err = g.saveManifest(state, plan.Target, plan.Request.WriteRoot, state.value)
				if err != nil {
					return state, recovered, err
				}
				recovered = append(recovered, intent.Path)
				binding = state.value.Bindings[plan.SourceID]
			}
		default:
			return state, recovered, fmt.Errorf("不支持的待恢复操作阶段：%s", intent.Phase)
		}
	}
	return state, recovered, nil
}

func createdBodyMatches(intent domain.MaterialsSyncIntent, document domain.FeishuTextDocument) bool {
	if intent.Placeholder {
		return strings.TrimSpace(document.Content) == ""
	}
	if intent.ExpectedContentHash != nil {
		return materialsContentHash(document.Content) == *intent.ExpectedContentHash
	}
	return document.Content == ""
}

func (g *feishuGateway) removeRecoveredEntry(state remoteMaterialsState, plan domain.MaterialsSyncPlan, intent domain.MaterialsSyncIntent) (remoteMaterialsState, error) {
	binding := state.value.Bindings[plan.SourceID]
	delete(binding.Entries, intent.Path)
	binding.Pending = removeMaterialIntent(binding.Pending, intent.OperationID)
	state.value.Bindings[plan.SourceID] = binding
	state.value.Generation++
	return g.saveManifest(state, plan.Target, plan.Request.WriteRoot, state.value)
}

func (g *feishuGateway) registerEntry(state remoteMaterialsState, plan domain.MaterialsSyncPlan, entry domain.MaterialsSyncEntry, intentID string) (remoteMaterialsState, error) {
	value := state.value
	binding := value.Bindings[plan.SourceID]
	binding.Entries[entry.Path] = entry
	if intentID != "" {
		binding.Pending = removeMaterialIntent(binding.Pending, intentID)
	}
	value.Bindings[plan.SourceID] = binding
	value.Generation++
	return g.saveManifest(state, plan.Target, plan.Request.WriteRoot, value)
}

func (g *feishuGateway) ensureDirectory(state remoteMaterialsState, plan domain.MaterialsSyncPlan, binding domain.MaterialsSyncBinding, path, title string) (remoteMaterialsState, error) {
	if _, exists := binding.Entries[path]; exists {
		return state, nil
	}
	parentEntry, ok := binding.Entries[materialsParentPath(path)]
	if !ok {
		return state, fmt.Errorf("飞书父目录尚未登记：%s", materialsParentPath(path))
	}
	parent := nodeFromEntry(parentEntry, plan.Target.SpaceID)
	intent, err := createIntentFor(state.value.Bindings[plan.SourceID], path, domain.MaterialsSyncDirectory, parent.NodeToken, title, "directory", false)
	if err != nil {
		return state, err
	}
	op := intent.OperationID
	temporary := intent.TemporaryTitle
	state, err = g.persistIntent(state, plan, intent)
	if err != nil {
		return state, err
	}
	if _, err := g.ownedMaterialNode(plan.Target, binding, parentEntry); err != nil {
		return state, err
	}
	intent.Phase = "create-attempted"
	state, err = g.persistIntent(state, plan, intent)
	if err != nil {
		return state, err
	}
	created, err := g.CreateDirectory(parent, temporary)
	if err != nil {
		return state, fmt.Errorf("飞书目录创建结果未确认，恢复记录已保存：%w", err)
	}
	intent.NodeToken = nonblank(created.Node.NodeToken)
	state, err = g.persistIntent(state, plan, intent)
	if err != nil {
		return state, fmt.Errorf("飞书目录已创建，记录恢复节点失败：%w", err)
	}
	renamed, err := g.Rename(created.Node, title)
	if err != nil {
		return state, fmt.Errorf("飞书目录已创建但重命名未确认，恢复记录已保存：%w", err)
	}
	version, err := g.Version(renamed.Node, materialsIncludeComments(path))
	if err != nil {
		return state, err
	}
	entry := domain.MaterialsSyncEntry{Path: path, Kind: domain.MaterialsSyncDirectory, NodeToken: renamed.Node.NodeToken, ObjectToken: renamed.Node.ObjectToken, ObjectType: renamed.Node.ObjectType, ParentNodeToken: renamed.Node.ParentNodeToken, Title: renamed.Node.Title, LocalHash: "directory", RemoteVersion: version}
	return g.registerEntry(state, plan, entry, op)
}

func (g *feishuGateway) ensureDirectoryOrMarkdown(state remoteMaterialsState, plan domain.MaterialsSyncPlan, scan localMaterialScan, path string, file localMaterial) (remoteMaterialsState, error) {
	binding := state.value.Bindings[plan.SourceID]
	if file.kind == domain.MaterialsSyncDirectory {
		return g.ensureDirectory(state, plan, binding, path, filepath.Base(filepath.FromSlash(path)))
	}
	parentEntry, ok := binding.Entries[materialsParentPath(path)]
	if !ok {
		return state, fmt.Errorf("飞书父目录尚未登记：%s", materialsParentPath(path))
	}
	parent := nodeFromEntry(parentEntry, plan.Target.SpaceID)
	title := filepath.Base(filepath.FromSlash(path))
	linksReady := true
	for _, linked := range file.linkedPaths {
		if _, exists := binding.Entries[linked]; !exists {
			linksReady = false
			break
		}
	}
	var previous *domain.MaterialsSyncIntent
	for _, pending := range binding.Pending {
		if pending.Path == path && (pending.Phase == "create" || pending.Phase == "create-attempted") {
			copy := pending
			previous = &copy
			break
		}
	}
	var markdown string
	var err error
	preparedMarkdown := linksReady && (previous == nil || previous.ExpectedContentHash != nil)
	if preparedMarkdown {
		markdown, err = readMarkdownLocal(scan.root, path)
		if err != nil {
			return state, err
		}
		markdown, err = rewriteRemoteMarkdown(scan, path, markdown, binding, plan.Request.TargetURL)
		if err != nil {
			return state, err
		}
	}
	completeCreation := preparedMarkdown && g.SupportsCompleteDocumentCreation(markdown) && materialsCompleteCreationWave(plan, path)
	placeholder := !completeCreation
	if previous != nil {
		placeholder = previous.Placeholder
	}
	intent, err := createIntentFor(binding, path, domain.MaterialsSyncMarkdown, parent.NodeToken, title, file.hash, placeholder)
	if err != nil {
		return state, err
	}
	if preparedMarkdown {
		hash := materialsContentHash(markdown)
		if previous != nil && (intent.ExpectedContentHash == nil || *intent.ExpectedContentHash != hash) {
			return state, errors.New("未完成的文档正文或引用已改变，请核对同步恢复记录")
		}
		intent.ExpectedContentHash = &hash
	}
	op := intent.OperationID
	temporary := intent.TemporaryTitle
	state, err = g.persistIntent(state, plan, intent)
	if err != nil {
		return state, err
	}
	if _, err := g.ownedMaterialNode(plan.Target, binding, parentEntry); err != nil {
		return state, err
	}
	intent.Phase = "create-attempted"
	state, err = g.persistIntent(state, plan, intent)
	if err != nil {
		return state, err
	}
	var created domain.FeishuWriteResult
	if intent.Placeholder {
		created, err = g.CreateDirectory(parent, temporary)
	} else {
		created, err = g.CreateCompleteDocument(parent, temporary, markdown, scan.root)
	}
	if err != nil {
		return state, fmt.Errorf("飞书 Markdown 文档创建结果未确认，恢复记录已保存：%w", err)
	}
	intent.NodeToken = nonblank(created.Node.NodeToken)
	state, err = g.persistIntent(state, plan, intent)
	if err != nil {
		return state, fmt.Errorf("飞书 Markdown 文档已创建，记录恢复节点失败：%w", err)
	}
	before, err := g.ReadDocument(created.Node, true)
	if err != nil {
		return state, err
	}
	if before.CommentsFingerprint != materialsHash("[]") || created.Version != nil && *created.Version != fmt.Sprint(before.Revision) ||
		(created.Version == nil || intent.Placeholder) && !createdBodyMatches(intent, before) {
		return state, errors.New("新建资料的正文、版本或评论已变化，已保留恢复记录")
	}
	bodyHash := materialsContentHash(before.Content)
	if !intent.Placeholder && intent.ExpectedContentHash != nil && *intent.ExpectedContentHash != bodyHash {
		// A confirmed import may normalize Markdown. Persist the exported body
		// baseline with the known node receipt before rename, as Source's journal does.
		value := state.value
		receiptBinding := value.Bindings[plan.SourceID]
		for index, pending := range receiptBinding.Pending {
			if pending.OperationID == intent.OperationID && pending.NodeToken != nil && *pending.NodeToken == created.Node.NodeToken {
				pending.ExpectedContentHash = &bodyHash
				receiptBinding.Pending[index] = pending
				break
			}
		}
		value.Bindings[plan.SourceID] = receiptBinding
		value.Generation++
		state, err = g.saveManifest(state, plan.Target, plan.Request.WriteRoot, value)
		if err != nil {
			return state, err
		}
	}
	renamed, err := g.Rename(created.Node, title)
	if err != nil {
		return state, fmt.Errorf("飞书 Markdown 文档已创建但重命名未确认，恢复记录已保存：%w", err)
	}
	after, err := g.ReadDocument(renamed.Node, true)
	if err != nil {
		return state, err
	}
	if materialsContentHash(after.Content) != bodyHash || after.CommentsFingerprint != before.CommentsFingerprint {
		return state, errors.New("新建资料在重命名期间正文或评论已变化，已保留恢复记录")
	}
	localHash := file.hash
	if intent.Placeholder {
		localHash = ""
	}
	version := domain.FeishuRemoteVersion{Version: fmt.Sprint(after.Revision), CommentsFingerprint: after.CommentsFingerprint}
	entry := domain.MaterialsSyncEntry{Path: path, Kind: domain.MaterialsSyncMarkdown, NodeToken: renamed.Node.NodeToken, ObjectToken: renamed.Node.ObjectToken, ObjectType: renamed.Node.ObjectType, ParentNodeToken: renamed.Node.ParentNodeToken, Title: renamed.Node.Title, LocalHash: localHash, RemoteVersion: version}
	return g.registerEntry(state, plan, entry, op)
}

func (g *feishuGateway) uploadNewFile(state remoteMaterialsState, plan domain.MaterialsSyncPlan, scan localMaterialScan, path string, file localMaterial) (remoteMaterialsState, error) {
	binding := state.value.Bindings[plan.SourceID]
	parentEntry, ok := binding.Entries[materialsParentPath(path)]
	if !ok {
		return state, fmt.Errorf("飞书父目录尚未登记：%s", materialsParentPath(path))
	}
	parent := nodeFromEntry(parentEntry, plan.Target.SpaceID)
	title := filepath.Base(filepath.FromSlash(path))
	intent, err := createIntentFor(binding, path, domain.MaterialsSyncFile, parent.NodeToken, title, file.hash, false)
	if err != nil {
		return state, err
	}
	op := intent.OperationID
	temporary := intent.TemporaryTitle
	state, err = g.persistIntent(state, plan, intent)
	if err != nil {
		return state, err
	}
	if _, err := g.ownedMaterialNode(plan.Target, binding, parentEntry); err != nil {
		return state, err
	}
	intent.Phase = "create-attempted"
	state, err = g.persistIntent(state, plan, intent)
	if err != nil {
		return state, err
	}
	created, err := g.UploadFile(parent, scan.root, path, temporary, nil, nil)
	if err != nil {
		return state, fmt.Errorf("飞书文件上传结果未确认，恢复记录已保存：%w", err)
	}
	intent.NodeToken = nonblank(created.Node.NodeToken)
	state, err = g.persistIntent(state, plan, intent)
	if err != nil {
		return state, fmt.Errorf("飞书文件已上传，记录恢复节点失败：%w", err)
	}
	before, err := g.Version(created.Node, true)
	if err != nil {
		return state, err
	}
	if before.CommentsFingerprint != materialsHash("[]") || created.Version != nil && before.Version != *created.Version {
		return state, errors.New("新建文件在登记前版本或评论已变化，已保留恢复记录")
	}
	renamed, err := g.Rename(created.Node, title)
	if err != nil {
		return state, fmt.Errorf("飞书文件已上传但重命名未确认，恢复记录已保存：%w", err)
	}
	version, err := g.Version(renamed.Node, true)
	if err != nil {
		return state, err
	}
	entry := domain.MaterialsSyncEntry{Path: path, Kind: domain.MaterialsSyncFile, NodeToken: renamed.Node.NodeToken, ObjectToken: renamed.Node.ObjectToken, ObjectType: renamed.Node.ObjectType, ParentNodeToken: renamed.Node.ParentNodeToken, Title: renamed.Node.Title, LocalHash: file.hash, RemoteVersion: version}
	return g.registerEntry(state, plan, entry, op)
}

func (g *feishuGateway) updateMaterial(state remoteMaterialsState, plan domain.MaterialsSyncPlan, scan localMaterialScan, file localMaterial, item domain.MaterialsSyncItem) (remoteMaterialsState, error) {
	binding := state.value.Bindings[plan.SourceID]
	entry, ok := binding.Entries[file.path]
	if !ok {
		return state, fmt.Errorf("远端资源尚未登记：%s", file.path)
	}
	if _, err := g.ownedMaterialNode(plan.Target, binding, entry); err != nil {
		return state, err
	}
	if item.ObservedNode != nil && !sameNodeIdentity(*item.ObservedNode, entry) {
		return state, errors.New("待覆盖远端节点身份与已登记资源不一致")
	}
	expected := item.ObservedVersion
	if expected == nil {
		expected = &entry.RemoteVersion
	}
	current, err := g.GetNode(entry.NodeToken)
	if err != nil || current == nil || !sameNodeIdentity(*current, entry) {
		return state, fmt.Errorf("待更新远端资源身份已改变：%s", file.path)
	}
	observed, err := g.Version(*current, materialsIncludeComments(file.path))
	if err != nil {
		return state, err
	}
	if observed != *expected {
		return state, fmt.Errorf("远端资源版本在同步前改变：%s", file.path)
	}
	op, err := newIntegrationUUID()
	if err != nil {
		return state, err
	}
	intent := domain.MaterialsSyncIntent{OperationID: op, Path: file.path, Kind: file.kind, ParentNodeToken: entry.ParentNodeToken, Title: entry.Title, TemporaryTitle: entry.Title + " [sw-" + op + "]", LocalHash: file.hash, Phase: "update", NodeToken: nonblank(entry.NodeToken), BaseVersion: expected}
	var markdown string
	if file.kind == domain.MaterialsSyncMarkdown {
		markdown, err = readMarkdownLocal(scan.root, file.path)
		if err != nil {
			return state, err
		}
		markdown, err = rewriteRemoteMarkdown(scan, file.path, markdown, binding, plan.Request.TargetURL)
		if err != nil {
			return state, err
		}
		hash := materialsContentHash(markdown)
		intent.ExpectedContentHash = &hash
	}
	state, err = g.persistIntent(state, plan, intent, item.Action == domain.MaterialsSyncConflict)
	if err != nil {
		return state, err
	}
	if file.kind == domain.MaterialsSyncFile {
		parent := nodeFromEntry(binding.Entries[materialsParentPath(file.path)], plan.Target.SpaceID)
		written, err := g.UploadFile(parent, scan.root, file.path, entry.Title, current, expected)
		if err != nil {
			return state, err
		}
		version, err := g.Version(written.Node, true)
		if err != nil {
			return state, err
		}
		entry.LocalHash, entry.RemoteVersion = file.hash, version
		return g.registerEntry(state, plan, entry, op)
	}
	revision, err := strconv.ParseInt(expected.Version, 10, 64)
	if err != nil || revision < 0 {
		return state, errors.New("飞书文档版本不是有效 revision_id")
	}
	parent := parentNodeFromEntries(binding, entry.ParentNodeToken, plan.Target)
	written, err := g.OverwriteDocument(*current, markdown, revision, scan.root, &expected.CommentsFingerprint, parent)
	if err != nil {
		return state, err
	}
	version, err := g.Version(written.Node, true)
	if err != nil {
		return state, err
	}
	entry.LocalHash, entry.RemoteVersion = file.hash, version
	return g.registerEntry(state, plan, entry, op)
}

func (g *feishuGateway) deleteMaterial(state remoteMaterialsState, plan domain.MaterialsSyncPlan, item domain.MaterialsSyncItem) (remoteMaterialsState, error) {
	if item.Path == materialsRequirementRoot || item.Path == materialsFilesRoot {
		return state, errors.New("受保护的飞书需求根或资料目录不能删除")
	}
	binding := state.value.Bindings[plan.SourceID]
	entry, ok := binding.Entries[item.Path]
	if !ok || item.Entry == nil || entry != *item.Entry {
		return state, fmt.Errorf("待删除资源与当前登记不一致：%s", item.Path)
	}
	current, err := g.GetNode(entry.NodeToken)
	if err != nil {
		return state, err
	}
	if current == nil {
		return g.removeDeletedMaterialEntry(state, plan, item.Path)
	}
	if _, err := g.ownedMaterialNode(plan.Target, binding, entry); err != nil {
		return state, err
	}
	if !sameNodeIdentity(*current, entry) {
		return state, fmt.Errorf("待删除远端资源身份已改变：%s", item.Path)
	}
	version, err := g.Version(*current, materialsIncludeComments(item.Path))
	if err != nil {
		return state, err
	}
	expected := item.ObservedVersion
	if expected == nil {
		expected = &entry.RemoteVersion
	}
	if version != *expected {
		return state, fmt.Errorf("待删除资源版本已改变：%s", item.Path)
	}
	for _, pending := range binding.Pending {
		if pending.Path == item.Path && pending.Phase == "update" {
			value := state.value
			binding.Pending = removeMaterialIntent(binding.Pending, pending.OperationID)
			value.Bindings[plan.SourceID] = binding
			value.Generation++
			state, err = g.saveManifest(state, plan.Target, plan.Request.WriteRoot, value)
			if err != nil {
				return state, err
			}
			binding = state.value.Bindings[plan.SourceID]
			break
		}
	}
	var intent domain.MaterialsSyncIntent
	for _, pending := range binding.Pending {
		if pending.Path == item.Path && pending.Phase == "delete" {
			if pending.NodeToken == nil || *pending.NodeToken != entry.NodeToken || pending.BaseVersion == nil || *pending.BaseVersion != version {
				return state, errors.New("待删除操作的节点或版本与当前登记不一致")
			}
			intent = pending
			break
		}
	}
	if intent.OperationID == "" {
		op, err := newIntegrationUUID()
		if err != nil {
			return state, err
		}
		intent = domain.MaterialsSyncIntent{OperationID: op, Path: item.Path, Kind: item.Kind, ParentNodeToken: entry.ParentNodeToken, Title: entry.Title, TemporaryTitle: entry.Title + " [sw-" + op + "]", LocalHash: entry.LocalHash, Phase: "delete", NodeToken: nonblank(entry.NodeToken), BaseVersion: &version}
	}
	state, err = g.persistIntent(state, plan, intent)
	if err != nil {
		return state, err
	}
	if err := g.DeleteNode(*current); err != nil {
		return state, err
	}
	return g.removeDeletedMaterialEntry(state, plan, item.Path)
}

func (g *feishuGateway) removeDeletedMaterialEntry(state remoteMaterialsState, plan domain.MaterialsSyncPlan, path string) (remoteMaterialsState, error) {
	binding := state.value.Bindings[plan.SourceID]
	delete(binding.Entries, path)
	pending := make([]domain.MaterialsSyncIntent, 0, len(binding.Pending))
	for _, intent := range binding.Pending {
		if intent.Path != path || intent.Phase != "delete" {
			pending = append(pending, intent)
		}
	}
	binding.Pending = pending
	state.value.Bindings[plan.SourceID] = binding
	state.value.Generation++
	return g.saveManifest(state, plan.Target, plan.Request.WriteRoot, state.value)
}

// Validate the registered chain up to the target before writing any resource.
// A child's own version cannot authorize writing through a changed directory.
func (g *feishuGateway) ownedMaterialNode(target domain.FeishuWikiNode, binding domain.MaterialsSyncBinding, entry domain.MaterialsSyncEntry) (domain.FeishuWikiNode, error) {
	ancestry := []domain.MaterialsSyncEntry{}
	seen := map[string]bool{}
	for {
		if seen[entry.NodeToken] {
			return domain.FeishuWikiNode{}, errors.New("同步清单目录关系存在循环")
		}
		seen[entry.NodeToken] = true
		ancestry = append(ancestry, entry)
		if entry.Path == materialsRequirementRoot {
			break
		}
		parent, ok := binding.Entries[materialsParentPath(entry.Path)]
		if !ok {
			return domain.FeishuWikiNode{}, errors.New("同步清单缺少父目录")
		}
		entry = parent
	}
	expectedParent := target.NodeToken
	var result domain.FeishuWikiNode
	for index := len(ancestry) - 1; index >= 0; index-- {
		registered := ancestry[index]
		node, err := g.GetNode(registered.NodeToken)
		if err != nil {
			return result, err
		}
		if node == nil {
			return result, errors.New("已登记的远端资料不存在")
		}
		if node.SpaceID != target.SpaceID || node.NodeToken != registered.NodeToken || node.NodeToken == target.NodeToken || node.ObjectToken != registered.ObjectToken || node.ObjectType != registered.ObjectType || node.ParentNodeToken != expectedParent || registered.ParentNodeToken != expectedParent {
			return result, errors.New("远端资料已移动或归属不明确，已保留")
		}
		if registered.Kind == domain.MaterialsSyncDirectory {
			version, err := g.Version(*node, materialsIncludeComments(registered.Path))
			if err != nil {
				return result, err
			}
			expected := registered.RemoteVersion
			if !materialsIncludeComments(registered.Path) {
				expected.CommentsFingerprint = ""
			}
			if node.Title != registered.Title || version != expected {
				return result, errors.New("需求根节点或资料目录在预览后被修改，已停止写入")
			}
		}
		expectedParent, result = node.NodeToken, *node
	}
	return result, nil
}

// The two protected control directories intentionally ignore comments. Every
// user material, including uploaded files and ordinary directories, compares
// both the revision and the comments fingerprint throughout plan/apply/recover.
func materialsIncludeComments(path string) bool {
	return path != materialsRequirementRoot && path != materialsFilesRoot
}

func nodeFromEntry(entry domain.MaterialsSyncEntry, space string) domain.FeishuWikiNode {
	return domain.FeishuWikiNode{SpaceID: space, NodeToken: entry.NodeToken, ObjectToken: entry.ObjectToken, ObjectType: entry.ObjectType, ParentNodeToken: entry.ParentNodeToken, Title: entry.Title}
}

func parentNodeFromEntries(binding domain.MaterialsSyncBinding, token string, target domain.FeishuWikiNode) *domain.FeishuWikiNode {
	for _, entry := range binding.Entries {
		if entry.NodeToken == token {
			node := nodeFromEntry(entry, target.SpaceID)
			return &node
		}
	}
	return nil
}

func materialsParentPath(path string) string {
	if path == materialsFilesRoot {
		return materialsRequirementRoot
	}
	if strings.Contains(path, "/") {
		return path[:strings.LastIndex(path, "/")]
	}
	return materialsFilesRoot
}

func removeMaterialIntent(values []domain.MaterialsSyncIntent, id string) []domain.MaterialsSyncIntent {
	result := values[:0]
	for _, value := range values {
		if value.OperationID != id {
			result = append(result, value)
		}
	}
	return result
}

func countPlanActions(plan domain.MaterialsSyncPlan, action domain.MaterialsSyncAction) int {
	count := 0
	for _, item := range plan.Items {
		if item.Action == action {
			count++
		}
	}
	return count
}

func readMarkdownLocal(root, relative string) (string, error) {
	path := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("资料不是普通文件：%s", relative)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func materialsContentHash(value string) string {
	value = strings.TrimRight(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	return materialsHash(value)
}

func newIntegrationUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}
