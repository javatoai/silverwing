package integrations

import (
	"errors"

	"silverwing-go/internal/domain"
)

// Before publishing READY, all bindings must describe the same protected root.
// Only the final initialization rename may account for a changed root version.
func (g *feishuGateway) validateInitializingMaterialsRoot(state remoteMaterialsState, plan domain.MaterialsSyncPlan) error {
	var expected *domain.MaterialsSyncEntry
	for _, binding := range state.value.Bindings {
		entry, ok := binding.Entries[materialsRequirementRoot]
		if !ok {
			return errors.New("初始化清单缺少受保护的需求根快照")
		}
		entry.RemoteVersion.CommentsFingerprint = ""
		if expected != nil && *expected != entry {
			return errors.New("初始化清单的需求根快照不唯一")
		}
		copy := entry
		expected = &copy
	}
	if expected == nil || expected.RemoteVersion.Version == "" {
		return errors.New("初始化清单缺少受保护的需求根快照")
	}
	root, err := g.GetNode(state.root.NodeToken)
	if err != nil {
		return err
	}
	if root == nil || *root != state.root || root.NodeToken != expected.NodeToken || root.ObjectToken != expected.ObjectToken || root.ObjectType != "docx" || root.ObjectType != expected.ObjectType || root.ParentNodeToken != plan.Target.NodeToken || root.SpaceID != plan.Target.SpaceID || (root.Title != plan.Request.RequirementTitle && root.Title != materialsCandidateTitle(plan.ScopeID, state.value.OperationID)) {
		return errors.New("需求根在初始化期间被修改或移动，已停止发布")
	}
	children, err := g.ListChildren(*root)
	if err != nil {
		return err
	}
	if len(children) != 1 || children[0] != state.control || state.control.Title != materialsManifestTitle || state.control.ObjectType != "docx" {
		return errors.New("初始化需求根中出现未登记内容或重复配置，已停止发布")
	}
	version, err := g.Version(*root, false)
	if err != nil {
		return err
	}
	completedRename := root.Title == plan.Request.RequirementTitle && expected.Title == materialsCandidateTitle(plan.ScopeID, state.value.OperationID)
	if version.Version != expected.RemoteVersion.Version && !completedRename {
		return errors.New("需求根在初始化期间的版本已改变，已停止发布")
	}
	return nil
}
