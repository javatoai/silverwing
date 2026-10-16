package integrations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

func (s *Service) openOrBootstrapMaterials(ctx context.Context, gateway *feishuGateway, plan domain.MaterialsSyncPlan) (remoteMaterialsState, error) {
	if err := ctx.Err(); err != nil {
		return remoteMaterialsState{}, err
	}
	if plan.Manifest != nil && plan.ManifestNode != nil && plan.ManifestRevision != nil && plan.RequirementRoot != nil {
		state := remoteMaterialsState{control: *plan.ManifestNode, revision: *plan.ManifestRevision, value: *plan.Manifest, root: *plan.RequirementRoot}
		return s.completeMaterialsBootstrap(gateway, state, plan)
	}
	if err := ctx.Err(); err != nil {
		return remoteMaterialsState{}, err
	}
	var receipt domain.MaterialsBootstrapRecovery
	if plan.DiscoverySnapshot != nil && plan.DiscoverySnapshot.Bootstrap != nil {
		receipt = *plan.DiscoverySnapshot.Bootstrap
	} else {
		operationID, err := newIntegrationUUID()
		if err != nil {
			return remoteMaterialsState{}, err
		}
		receipt = domain.MaterialsBootstrapRecovery{ScopeID: plan.ScopeID, OperationID: operationID,
			CandidateTitle: materialsCandidateTitle(plan.ScopeID, operationID), Phase: "CREATE_ROOT"}
	}
	if err := s.saveMaterialsBootstrap(&receipt); err != nil {
		return remoteMaterialsState{}, err
	}
	if receipt.Root == nil {
		created, err := gateway.CreateDirectory(plan.Target, receipt.CandidateTitle)
		if err != nil {
			if process.IsCleanupFailure(err) {
				return remoteMaterialsState{}, err
			}
			var unconfirmed *materialsUnconfirmedCreationError
			if ctx.Err() == nil && !errors.As(err, &unconfirmed) {
				children, recoveryErr := gateway.ListChildren(plan.Target)
				if process.IsCleanupFailure(recoveryErr) {
					return remoteMaterialsState{}, recoveryErr
				}
				if recoveryErr == nil {
					recovered, recoveryErr := gateway.materialsBootstrapRoot(plan.Target, plan.Request.RequirementTitle, &receipt, children)
					if recoveryErr == nil && recovered != nil {
						created.Node, err = *recovered, nil
					}
				}
			}
			if err != nil {
				receipt.Phase = "UNCONFIRMED_CREATE"
				// Keep the pre-write identity even when the command outcome is unknown.
				saveErr := s.saveMaterialsBootstrap(&receipt)
				if ctx.Err() != nil {
					err = ctx.Err()
				}
				return remoteMaterialsState{}, fmt.Errorf("飞书候选需求根创建结果未确认，请先恢复检查：%w", errors.Join(err, saveErr))
			}
		}
		receipt.Root = &created.Node
	}
	remoteRoot := *receipt.Root
	receipt.Phase, receipt.Error = "ROOT_CREATED", ""
	if err := s.saveMaterialsBootstrap(&receipt); err != nil {
		return remoteMaterialsState{}, err
	}
	children, err := gateway.ListChildren(remoteRoot)
	if err != nil {
		return remoteMaterialsState{}, err
	}
	if len(children) != 0 {
		return remoteMaterialsState{}, errors.New("初始化期间候选需求根的子节点已改变，请重新预览恢复")
	}
	version, err := gateway.Version(remoteRoot, false)
	if err != nil {
		return remoteMaterialsState{}, err
	}
	rootEntry := domain.MaterialsSyncEntry{Path: materialsRequirementRoot, Kind: domain.MaterialsSyncDirectory, NodeToken: remoteRoot.NodeToken, ObjectToken: remoteRoot.ObjectToken, ObjectType: remoteRoot.ObjectType, ParentNodeToken: remoteRoot.ParentNodeToken, Title: remoteRoot.Title, LocalHash: "directory", RemoteVersion: version}
	initial := domain.MaterialsSyncManifest{FormatVersion: 2, Manager: "SilverWing", Namespace: "silverwing-materials-sync", TargetNodeToken: plan.Target.NodeToken, SpaceID: plan.Target.SpaceID, ScopeID: plan.ScopeID, RequirementNodeToken: remoteRoot.NodeToken, State: domain.MaterialsSyncInitializing, OperationID: receipt.OperationID, Generation: 1, Bindings: map[string]domain.MaterialsSyncBinding{
		plan.SourceID: {SourceID: plan.SourceID, RequirementID: plan.Request.RequirementID, RequirementTitle: plan.Request.RequirementTitle, Subdirectory: plan.Request.Subdirectory, Entries: map[string]domain.MaterialsSyncEntry{materialsRequirementRoot: rootEntry}, Pending: []domain.MaterialsSyncIntent{}},
	}}
	receipt.Manifest, receipt.Phase = &initial, "CREATE_MANIFEST"
	if err := s.saveMaterialsBootstrap(&receipt); err != nil {
		return remoteMaterialsState{}, err
	}
	control, revision, err := gateway.CreateManifest(remoteRoot, initial, plan.Request.WriteRoot)
	if err != nil {
		return remoteMaterialsState{}, fmt.Errorf("飞书同步清单创建结果未确认，请先恢复检查：%w", err)
	}
	state := remoteMaterialsState{control: control, revision: revision, value: initial, root: remoteRoot}
	return s.completeMaterialsBootstrap(gateway, state, plan)
}

func (s *Service) completeMaterialsBootstrap(gateway *feishuGateway, state remoteMaterialsState, plan domain.MaterialsSyncPlan) (remoteMaterialsState, error) {
	state, err := gateway.finishBootstrap(state, plan)
	if err != nil {
		return remoteMaterialsState{}, err
	}
	receipt, err := s.readMaterialsBootstrap(plan.ScopeID)
	if err != nil {
		return state, err
	}
	if receipt != nil {
		if receipt.OperationID != state.value.OperationID {
			return state, errors.New("初始化恢复记录已被其他操作替换")
		}
		path, err := s.materialsBootstrapPath(plan.ScopeID)
		if err != nil {
			return state, err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return state, fmt.Errorf("清理已完成的初始化记录：%w", err)
		}
	}
	return state, nil
}

func (g *feishuGateway) finishBootstrap(state remoteMaterialsState, plan domain.MaterialsSyncPlan) (remoteMaterialsState, error) {
	if state.value.State == domain.MaterialsSyncReady {
		return state, nil
	}
	if state.value.State != domain.MaterialsSyncInitializing || state.value.ScopeID != plan.ScopeID {
		return state, errors.New("飞书同步初始化清单不属于当前目标")
	}
	if err := g.validateInitializingMaterialsRoot(state, plan); err != nil {
		return state, err
	}
	if state.root.Title != plan.Request.RequirementTitle {
		renamed, err := g.Rename(state.root, plan.Request.RequirementTitle)
		if err != nil {
			return state, err
		}
		state.root = renamed.Node
	}
	version, err := g.Version(state.root, false)
	if err != nil {
		return state, err
	}
	for source, binding := range state.value.Bindings {
		entry := binding.Entries[materialsRequirementRoot]
		entry.Title = state.root.Title
		entry.RemoteVersion = version
		binding.Entries[materialsRequirementRoot] = entry
		state.value.Bindings[source] = binding
	}
	state.value.State = domain.MaterialsSyncReady
	state.value.Generation++
	return g.saveManifest(state, plan.Target, plan.Request.WriteRoot, state.value)
}
