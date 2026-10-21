package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"silverwing-go/internal/domain"
)

// This journal belongs to Go. A local receipt is useful only while the remote
// manifest still contains its original durable intent; it never grants ownership.
type materialsReceiptJournalValue struct {
	FormatVersion int                                   `json:"formatVersion"`
	Namespace     string                                `json:"namespace"`
	SpaceID       string                                `json:"spaceId"`
	TargetToken   string                                `json:"targetToken"`
	RootToken     string                                `json:"rootToken"`
	ScopeID       string                                `json:"scopeId"`
	SourceID      string                                `json:"sourceId"`
	Receipts      map[string]materialsOperationReceipt  `json:"receipts"`
	Unconfirmed   map[string]domain.MaterialsSyncIntent `json:"unconfirmed"`
}

type materialsOperationReceipt struct {
	Intent      domain.MaterialsSyncIntent `json:"intent"`
	Result      domain.FeishuWriteResult   `json:"result"`
	Complete    bool                       `json:"complete"`
	ContentHash *string                    `json:"contentHash,omitempty"`
}

type materialsReceiptJournal struct {
	mu      sync.Mutex
	value   materialsReceiptJournalValue
	persist func(materialsReceiptJournalValue) error
	failure error
}

func newMaterialsReceiptJournalValue(plan domain.MaterialsSyncPlan, state remoteMaterialsState) materialsReceiptJournalValue {
	return materialsReceiptJournalValue{FormatVersion: 1, Namespace: state.value.Namespace, SpaceID: plan.Target.SpaceID,
		TargetToken: plan.Target.NodeToken, RootToken: state.root.NodeToken, ScopeID: plan.ScopeID, SourceID: plan.SourceID,
		Receipts: map[string]materialsOperationReceipt{}, Unconfirmed: map[string]domain.MaterialsSyncIntent{}}
}

func (s *Service) openMaterialsReceiptJournal(plan domain.MaterialsSyncPlan, state remoteMaterialsState) (*materialsReceiptJournal, error) {
	expected := newMaterialsReceiptJournalValue(plan, state)
	name := "receipts-" + materialsHash(plan.ScopeID+"\n"+plan.SourceID) + ".json"
	path := filepath.Join(s.syncStateDirectory(), name)
	journal := &materialsReceiptJournal{value: expected, persist: func(value materialsReceiptJournalValue) error {
		return writeAtomicJSON(path, value)
	}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return journal, nil
	}
	if err != nil {
		return nil, materialsManifestStateFailure(fmt.Errorf("读取资料写入回执失败：%w", err))
	}
	if err := json.Unmarshal(data, &journal.value); err != nil {
		return nil, materialsManifestStateFailure(fmt.Errorf("资料写入回执损坏：%w", err))
	}
	a, b := journal.value, expected
	if a.FormatVersion != b.FormatVersion || a.Namespace != b.Namespace || a.SpaceID != b.SpaceID ||
		a.TargetToken != b.TargetToken || a.RootToken != b.RootToken || a.ScopeID != b.ScopeID || a.SourceID != b.SourceID ||
		a.Receipts == nil || a.Unconfirmed == nil {
		return nil, materialsManifestStateFailure(errors.New("资料写入回执不属于当前需求、源或飞书目标"))
	}
	return journal, nil
}

func materialsJournalIntentMatches(a, b domain.MaterialsSyncIntent) bool {
	if a.Phase == "create-attempted" {
		a.Phase = "create"
	}
	if b.Phase == "create-attempted" {
		b.Phase = "create"
	}
	a.NodeToken, b.NodeToken = nil, nil
	return sameMaterialsIntent(a, b)
}

func (j *materialsReceiptJournal) healthy() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.failure
}

func (j *materialsReceiptJournal) find(intent domain.MaterialsSyncIntent) (materialsOperationReceipt, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.failure != nil {
		return materialsOperationReceipt{}, false, j.failure
	}
	receipt, ok := j.value.Receipts[intent.OperationID]
	if !ok {
		return receipt, false, nil
	}
	if !materialsJournalIntentMatches(receipt.Intent, intent) ||
		intent.NodeToken != nil && *intent.NodeToken != receipt.Result.Node.NodeToken {
		return receipt, false, materialsManifestStateFailure(errors.New("资料本地回执与远端待恢复操作不一致"))
	}
	if err := validateMaterialsReceiptNode(j.value.SpaceID, intent, receipt.Result.Node); err != nil {
		return receipt, false, materialsManifestStateFailure(err)
	}
	return receipt, true, nil
}

func validateMaterialsReceiptNode(space string, intent domain.MaterialsSyncIntent, node domain.FeishuWikiNode) error {
	typeName := "docx"
	if intent.Kind == domain.MaterialsSyncFile {
		typeName = "file"
	}
	titleMatches := node.Title == intent.Title || node.Title == intent.TemporaryTitle || intent.Phase == "update" && node.Title != ""
	if node.SpaceID != space || node.NodeToken == "" || node.ObjectToken == "" || node.ObjectType != typeName ||
		node.ParentNodeToken != intent.ParentNodeToken || !titleMatches ||
		intent.NodeToken != nil && *intent.NodeToken != node.NodeToken {
		return fmt.Errorf("写入回执中的节点身份、位置或标题不一致：%s", intent.Path)
	}
	return nil
}

func (j *materialsReceiptJournal) saveLocked() error {
	if j.failure != nil {
		return j.failure
	}
	if err := j.persist(j.value); err != nil {
		j.failure = materialsManifestStateFailure(fmt.Errorf("资料写入回执未能耐久保存，已停止后续写入：%w", err))
		return j.failure
	}
	return nil
}

// Do not inspect cancellation before recording a response. Even a response that
// arrives just after cancellation must retain its allocated token for recovery.
func (j *materialsReceiptJournal) record(intent domain.MaterialsSyncIntent, result domain.FeishuWriteResult, complete bool, contentHash *string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.failure != nil {
		return j.failure
	}
	if err := validateMaterialsReceiptNode(j.value.SpaceID, intent, result.Node); err != nil {
		return materialsManifestStateFailure(err)
	}
	previous, exists := j.value.Receipts[intent.OperationID]
	if exists && (!materialsJournalIntentMatches(previous.Intent, intent) || previous.Result.Node.NodeToken != result.Node.NodeToken ||
		previous.Result.Node.ObjectToken != result.Node.ObjectToken || previous.Result.Node.ObjectType != result.Node.ObjectType) {
		return materialsManifestStateFailure(errors.New("同一资料操作返回了不同的资源回执"))
	}
	if result.Version == nil && exists {
		result.Version = previous.Result.Version
	}
	if result.CommentsFingerprint == nil && exists {
		result.CommentsFingerprint = previous.Result.CommentsFingerprint
	}
	if contentHash == nil && result.VerifiedDocument != nil {
		hash := materialsContentHash(result.VerifiedDocument.Content)
		contentHash = &hash
	}
	if contentHash == nil && exists {
		contentHash = previous.ContentHash
	}
	if complete && intent.Phase == "update" && contentHash == nil {
		contentHash = intent.ExpectedContentHash
	}
	j.value.Receipts[intent.OperationID] = materialsOperationReceipt{Intent: intent, Result: result,
		Complete: complete || previous.Complete, ContentHash: contentHash}
	delete(j.value.Unconfirmed, intent.OperationID)
	return j.saveLocked()
}

func (j *materialsReceiptJournal) markUnconfirmed(intent domain.MaterialsSyncIntent) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.value.Unconfirmed[intent.OperationID] = intent
	return j.saveLocked()
}

func (j *materialsReceiptJournal) requireConfirmedCreate(intent domain.MaterialsSyncIntent) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.failure != nil {
		return j.failure
	}
	if pending, ok := j.value.Unconfirmed[intent.OperationID]; ok {
		if !materialsJournalIntentMatches(pending, intent) {
			return materialsManifestStateFailure(errors.New("未确认创建记录与当前操作不一致"))
		}
		return fmt.Errorf("上次创建结果尚未确认，已保留恢复记录：%s", intent.Path)
	}
	return nil
}

// Pruning is allowed only after a verified remote checkpoint, with no workers
// still running. A failed checkpoint leaves every receipt available next time.
func (j *materialsReceiptJournal) prune(binding domain.MaterialsSyncBinding) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	pending := map[string]bool{}
	for _, intent := range binding.Pending {
		pending[intent.OperationID] = true
	}
	changed := false
	for id, receipt := range j.value.Receipts {
		entry, registered := binding.Entries[receipt.Intent.Path]
		if !pending[id] && registered && entry.NodeToken == receipt.Result.Node.NodeToken && entry.ObjectToken == receipt.Result.Node.ObjectToken {
			delete(j.value.Receipts, id)
			changed = true
		}
	}
	for id := range j.value.Unconfirmed {
		if !pending[id] {
			delete(j.value.Unconfirmed, id)
			changed = true
		}
	}
	if changed {
		return j.saveLocked()
	}
	return j.failure
}

func (g *feishuGateway) recoverMaterialsReceipts(state remoteMaterialsState, plan domain.MaterialsSyncPlan, scan localMaterialScan, approval domain.MaterialsSyncApproval) (remoteMaterialsState, []string, error) {
	ops, err := g.materialsOperations(plan, scan, state)
	if err != nil {
		return state, nil, err
	}
	return recoverMaterialsReceipts(g.ctx, ops, state, plan, scan, approval)
}

// Reconcile update responses before intent-only recovery. Creation receipts run
// in the coordinator's depth waves, so parents register before descendants and
// an ordinary conflict cannot stop independent siblings.
func recoverMaterialsReceipts(ctx context.Context, ops materialsResourceOperations, state remoteMaterialsState,
	plan domain.MaterialsSyncPlan, scan localMaterialScan, approval domain.MaterialsSyncApproval) (remoteMaterialsState, []string, error) {
	binding, exists := state.value.Bindings[plan.SourceID]
	if !exists || len(binding.Pending) == 0 {
		return state, nil, nil
	}
	pending := append([]domain.MaterialsSyncIntent(nil), binding.Pending...)
	recovered := []string{}
	checkpoint := func(binding domain.MaterialsSyncBinding) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := ops.journal.healthy(); err != nil {
			return err
		}
		value := cloneMaterialsManifest(state.value)
		value.Bindings[plan.SourceID] = cloneMaterialsBinding(binding)
		value.Generation++
		next, err := ops.checkpoint(state, value)
		if err != nil {
			return materialsManifestStateFailure(err)
		}
		state = next
		return ops.journal.prune(state.value.Bindings[plan.SourceID])
	}
	for _, intent := range pending {
		if err := ctx.Err(); err != nil {
			return state, recovered, err
		}
		if !materialsPlanIncludesPath(plan.Request, scan, intent.Path) {
			continue
		}
		if intent.Phase != "update" {
			continue
		}
		receipt, found, err := ops.journal.find(intent)
		if err != nil {
			return state, recovered, err
		}
		if !found {
			continue
		}
		recoveryErr := func() error {
			file, exists := scan.files[intent.Path]
			if intent.Path == materialsFilesRoot {
				file, exists = localMaterial{path: intent.Path, kind: domain.MaterialsSyncDirectory}, true
			}
			if !exists || file.kind != intent.Kind || file.hash != intent.LocalHash {
				return fmt.Errorf("已知回执对应的本地资料已改变：%s", intent.Path)
			}
			if err := ops.verify(file); err != nil {
				return err
			}
			binding = cloneMaterialsBinding(state.value.Bindings[plan.SourceID])
			parent, exists := binding.Entries[materialsParentPath(intent.Path)]
			if !exists || parent.NodeToken != intent.ParentNodeToken {
				return errors.New("回执操作的父目录与远端清单不一致")
			}
			gateway := ops.worker(ctx)
			if _, err := newMaterialsSnapshots(gateway).owned(plan.Target, binding, parent); err != nil {
				return err
			}
			node, err := gateway.GetNode(receipt.Result.Node.NodeToken)
			if err != nil {
				return err
			}
			if node == nil || node.SpaceID != plan.Target.SpaceID || node.NodeToken != receipt.Result.Node.NodeToken ||
				node.ObjectToken != receipt.Result.Node.ObjectToken || node.ObjectType != receipt.Result.Node.ObjectType || node.ParentNodeToken != intent.ParentNodeToken {
				return fmt.Errorf("本地回执登记的资源已删除或改变归属：%s", intent.Path)
			}
			if err := validateMaterialsReceiptNode(plan.Target.SpaceID, intent, *node); err != nil {
				return err
			}
			version, err := gateway.Version(*node, materialsIncludeComments(intent.Path))
			if err != nil {
				return err
			}
			expectedComments := materialsHash("[]")
			if intent.BaseVersion != nil {
				expectedComments = intent.BaseVersion.CommentsFingerprint
			}
			if receipt.Result.CommentsFingerprint != nil {
				expectedComments = *receipt.Result.CommentsFingerprint
			}
			if materialsIncludeComments(intent.Path) && version.CommentsFingerprint != expectedComments {
				return errors.New("回执登记的资源评论已被人工修改")
			}
			var contentHash *string
			if intent.Kind != domain.MaterialsSyncFile {
				body, err := gateway.ReadDocument(*node, false)
				if err != nil {
					return err
				}
				wanted := receipt.ContentHash
				if wanted == nil && receipt.Complete {
					wanted = intent.ExpectedContentHash
				}
				if wanted == nil {
					blank := materialsContentHash("")
					wanted = &blank
				}
				actual := materialsContentHash(body.Content)
				if actual != *wanted || strconv.FormatInt(body.Revision, 10) != version.Version {
					return errors.New("已知写入回执的正文或版本已被修改")
				}
				contentHash = &actual
			} else {
				reader, ok := gateway.(domain.FeishuFileFingerprintReader)
				if !ok {
					return errors.New("文件回执缺少内容恢复核对能力")
				}
				proof, err := reader.ReadFileFingerprint(*node)
				if err != nil {
					return err
				}
				if proof.SHA256 != intent.LocalHash || proof.RemoteVersion != version {
					return errors.New("文件回执的内容或版本已被修改")
				}
			}
			if receipt.Result.Version != nil && version.Version != *receipt.Result.Version {
				// A lost rename response can change a docx revision, but only its exact
				// known body, unchanged comments and final title prove that transition.
				if intent.Kind == domain.MaterialsSyncFile || node.Title != intent.Title || receipt.Result.Node.Title == intent.Title || contentHash == nil {
					return errors.New("已知写入回执的远端版本已改变")
				}
			}
			entry, registered := binding.Entries[intent.Path]
			if intent.Phase == "update" {
				if !registered || intent.NodeToken == nil || intent.BaseVersion == nil || !materialsSameIdentity(*node, entry, plan.Target.SpaceID) ||
					entry.Title != intent.Title || entry.Kind != intent.Kind || !receipt.Complete {
					return errors.New("更新回执与已登记资料不一致")
				}
				if node.Title != entry.Title {
					if _, approved := approval.OverwritePaths[intent.Path]; !approved {
						return errors.New("恢复人工修改的标题需要重新确认覆盖")
					}
					if node.Title != receipt.Result.Node.Title {
						return errors.New("更新回执的标题又被人工修改")
					}
				}
			} else if registered {
				return errors.New("创建回执的路径已被其它操作登记")
			}
			// Record the proved baseline locally, then make its token durable before
			// the next title write. Cancellation never flushes an unproved entry.
			if err := ops.journal.record(intent, domain.FeishuWriteResult{Node: *node, Version: &version.Version, CommentsFingerprint: &version.CommentsFingerprint}, receipt.Complete, contentHash); err != nil {
				return err
			}
			if intent.NodeToken == nil {
				for index := range binding.Pending {
					if binding.Pending[index].OperationID == intent.OperationID {
						token := node.NodeToken
						binding.Pending[index].NodeToken = &token
					}
				}
				if err := checkpoint(binding); err != nil {
					return err
				}
				binding = cloneMaterialsBinding(state.value.Bindings[plan.SourceID])
			}
			if node.Title != intent.Title {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := ops.journal.healthy(); err != nil {
					return err
				}
				before, err := gateway.Version(*node, materialsIncludeComments(intent.Path))
				if err != nil {
					return err
				}
				if before != version {
					return errors.New("恢复改名前资源又被修改")
				}
				written, err := gateway.Rename(*node, intent.Title)
				if err != nil {
					return err
				}
				written.CommentsFingerprint = &version.CommentsFingerprint
				if err := ops.journal.record(intent, written, receipt.Complete, contentHash); err != nil {
					return err
				}
				if err := validateMaterialsReceiptNode(plan.Target.SpaceID, intent, written.Node); err != nil {
					return err
				}
				if written.Node.Title != intent.Title {
					return errors.New("恢复改名后的标题不一致")
				}
				node = &written.Node
				version, err = gateway.Version(*node, materialsIncludeComments(intent.Path))
				if err != nil {
					return err
				}
				if version.CommentsFingerprint != before.CommentsFingerprint {
					return errors.New("恢复改名期间评论改变")
				}
				if intent.Kind != domain.MaterialsSyncFile {
					body, err := gateway.ReadDocument(*node, false)
					if err != nil {
						return err
					}
					if contentHash == nil || materialsContentHash(body.Content) != *contentHash || strconv.FormatInt(body.Revision, 10) != version.Version {
						return errors.New("恢复改名期间正文改变")
					}
				} else if written.Version != nil && version.Version != *written.Version {
					return errors.New("恢复改名期间文件版本改变")
				}
				if err := ops.journal.record(intent, domain.FeishuWriteResult{Node: *node, Version: &version.Version, CommentsFingerprint: &version.CommentsFingerprint}, receipt.Complete, contentHash); err != nil {
					return err
				}
			}
			if err := ops.verify(file); err != nil {
				return err
			}
			if intent.Phase != "update" {
				entry = domain.MaterialsSyncEntry{Path: intent.Path, Kind: intent.Kind, NodeToken: node.NodeToken, ObjectToken: node.ObjectToken,
					ObjectType: node.ObjectType, ParentNodeToken: node.ParentNodeToken, Title: intent.Title}
			}
			entry.LocalHash, entry.RemoteVersion = intent.LocalHash, version
			if !receipt.Complete {
				entry.LocalHash = ""
			}
			binding.Entries[intent.Path] = entry
			binding.Pending = removeMaterialIntent(binding.Pending, intent.OperationID)
			if err := checkpoint(binding); err != nil {
				return err
			}
			return nil
		}()
		if recoveryErr != nil {
			// A known update receipt that no longer proves the observed remote
			// state is left pending for the reviewed conflict path. It cannot
			// block independent siblings or authorize a replay/overwrite.
			if intent.Phase == "update" && !materialsFatalWrite(recoveryErr) {
				continue
			}
			return state, recovered, recoveryErr
		}
		recovered = append(recovered, intent.Path)
	}
	return state, recovered, nil
}
