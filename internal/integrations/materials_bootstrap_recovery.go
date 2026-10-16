package integrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func materialsCandidateTitle(scope, operation string) string {
	return "SilverWing 候选需求根 " + scope[:16] + " [" + operation + "]"
}

func (s *Service) materialsBootstrapPath(scope string) (string, error) {
	if len(scope) != 64 || strings.Trim(scope, "0123456789abcdef") != "" {
		return "", errors.New("资料同步恢复范围无效")
	}
	return filepath.Join(s.syncStateDirectory(), "bootstrap-"+scope+".json"), nil
}

func (s *Service) readMaterialsBootstrap(scope string) (*domain.MaterialsBootstrapRecovery, error) {
	path, err := s.materialsBootstrapPath(scope)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取资料同步恢复记录：%w", err)
	}
	var receipt domain.MaterialsBootstrapRecovery
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return nil, fmt.Errorf("资料同步恢复记录损坏：%w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, errors.New("资料同步恢复记录包含多余内容")
	}
	if receipt.ScopeID != scope || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(receipt.OperationID) || receipt.CandidateTitle != materialsCandidateTitle(scope, receipt.OperationID) {
		return nil, errors.New("资料同步恢复记录的范围或操作标识不一致")
	}
	switch receipt.Phase {
	case "CREATE_ROOT", "UNCONFIRMED_CREATE":
	case "ROOT_CREATED", "CREATE_MANIFEST":
		if receipt.Root == nil {
			return nil, errors.New("资料同步恢复记录缺少已创建的根节点")
		}
	default:
		return nil, errors.New("资料同步恢复阶段不受支持")
	}
	return &receipt, nil
}

func (s *Service) saveMaterialsBootstrap(receipt *domain.MaterialsBootstrapRecovery) error {
	path, err := s.materialsBootstrapPath(receipt.ScopeID)
	if err != nil {
		return err
	}
	return writeAtomicJSON(path, receipt)
}

func (s *Service) lockMaterialsScope(scope string) (func(), error) {
	path, err := s.materialsBootstrapPath(scope)
	if err != nil {
		return nil, err
	}
	release, err := storage.AcquireLock(strings.TrimSuffix(path, ".json") + ".lock")
	if err != nil {
		return nil, fmt.Errorf("该需求范围正在同步或无法取得同步锁：%w", err)
	}
	return release, nil
}

// Discovery and recovery previews only read local receipts and remote nodes.
// Creation and repair remain behind the confirmed sync action.
func (s *Service) discoverMaterials(g *feishuGateway, target domain.FeishuWikiNode, scope, title string) (domain.MaterialsSyncDiscoverySnapshot, error) {
	receipt, err := s.readMaterialsBootstrap(scope)
	if err != nil {
		return domain.MaterialsSyncDiscoverySnapshot{}, err
	}
	return g.discover(target, scope, title, receipt)
}

func (g *feishuGateway) materialsBootstrapRoot(target domain.FeishuWikiNode, title string, receipt *domain.MaterialsBootstrapRecovery, children []domain.FeishuWikiNode) (*domain.FeishuWikiNode, error) {
	if receipt == nil {
		return nil, nil
	}
	var found *domain.FeishuWikiNode
	for _, node := range children {
		if node.Title == receipt.CandidateTitle || receipt.Root != nil && node.NodeToken == receipt.Root.NodeToken {
			if found != nil {
				return nil, errors.New("恢复记录对应多个飞书候选需求根，无法确定归属")
			}
			copy := node
			found = &copy
		}
	}
	if receipt.Root != nil {
		current, err := g.GetNode(receipt.Root.NodeToken)
		if err != nil {
			return nil, err
		}
		if current == nil || found == nil || *current != *found || current.ObjectToken != receipt.Root.ObjectToken || current.ObjectType != receipt.Root.ObjectType || current.SpaceID != receipt.Root.SpaceID || current.ParentNodeToken != receipt.Root.ParentNodeToken {
			return nil, errors.New("恢复记录中的飞书需求根已移动、被替换或不存在")
		}
	}
	if found == nil {
		return nil, nil
	}
	if found.NodeToken == "" || found.ObjectToken == "" || found.SpaceID != target.SpaceID || found.ParentNodeToken != target.NodeToken || found.ObjectType != "docx" || (found.Title != receipt.CandidateTitle && found.Title != title) {
		return nil, errors.New("飞书候选需求根与恢复记录的身份或位置不一致")
	}
	if receipt.Manifest != nil {
		value := receipt.Manifest
		if domain.ValidateMaterialsSyncManager(*value) != nil || value.ScopeID != receipt.ScopeID || value.OperationID != receipt.OperationID || value.SpaceID != target.SpaceID || value.TargetNodeToken != target.NodeToken || value.RequirementNodeToken != found.NodeToken || value.State != domain.MaterialsSyncInitializing {
			return nil, errors.New("恢复记录中的初始化清单不属于当前需求根")
		}
	}
	return found, nil
}

func initializingMaterialsRootTitle(value domain.MaterialsSyncManifest, binding domain.MaterialsSyncBinding, entry domain.MaterialsSyncEntry, root domain.FeishuWikiNode) bool {
	return value.State == domain.MaterialsSyncInitializing && len(value.ScopeID) == 64 &&
		entry.Title == materialsCandidateTitle(value.ScopeID, value.OperationID) && root.Title == binding.RequirementTitle
}
