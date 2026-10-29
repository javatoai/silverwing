package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"silverwing-go/internal/domain"
)

type tagProbeSnapshotKey struct{}

// TagProbeSnapshot is an authoritative, single-round read. Every service lane
// sees the same complete candidate set, including unselected newer releases.
// It has no lifetime beyond the caller's round and never replaces the CAS read.
type TagProbeSnapshot struct {
	scope      [32]byte
	tasks      []tagProbeSnapshotTask
	byID       map[string][]tagProbeSnapshotOperation
	candidates map[string][]genbuCandidate
}

type tagProbeSnapshotOperation struct {
	task, operation int
}

type tagProbeSnapshotTask struct {
	id, directory string
	operations    []domain.TagOperation
}

type TagProbeHistoryRecord struct {
	TaskID string
	Report TagOperationReport
}

func tagProbeSnapshotScope(config domain.AppConfig, tasks []domain.TaskManifest) [32]byte {
	data, _ := json.Marshal(struct {
		Config domain.AppConfig
		Tasks  []domain.TaskManifest
	}{config, tasks})
	return sha256.Sum256(data)
}

func (s *Service) ReadTagProbeSnapshot(ctx context.Context, config domain.AppConfig, tasks []domain.TaskManifest) (*TagProbeSnapshot, error) {
	snapshot := &TagProbeSnapshot{scope: tagProbeSnapshotScope(config, tasks), byID: make(map[string][]tagProbeSnapshotOperation), candidates: make(map[string][]genbuCandidate)}
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		directory, err := historyTaskDirectory(config, task)
		if err != nil {
			return nil, err
		}
		operations, err := s.store.list(directory)
		if err != nil {
			return nil, err
		}
		snapshot.tasks = append(snapshot.tasks, tagProbeSnapshotTask{task.TaskDirectoryName, directory, operations})
		services := make(map[string]domain.GroupServiceConfig)
		if group, groupErr := config.Group(task.GroupID); groupErr == nil {
			for _, service := range group.Services {
				services[service.ID] = service
			}
		}
		for operationIndex, operation := range operations {
			snapshot.byID[operation.OperationID] = append(snapshot.byID[operation.OperationID], tagProbeSnapshotOperation{len(snapshot.tasks) - 1, operationIndex})
			service, ok := services[operation.GroupServiceID]
			if ok && service.GenbuProbeEnabled && domain.TagGenbuProbeEligible(operation.State, value(operation.Tag)) {
				snapshot.candidates[service.GenbuServiceName] = append(snapshot.candidates[service.GenbuServiceName], genbuCandidate{directory, operation, service.GenbuServiceName})
			}
		}
	}
	return snapshot, ctx.Err()
}

// RecordsForIDs projects only the requested rows while the private candidate
// set stays complete for newer-release ordering. A single status poll never
// clones or constructs every history report merely to select one operation.
func (s *TagProbeSnapshot) RecordsForIDs(ids []string) []TagProbeHistoryRecord {
	var records []TagProbeHistoryRecord
	for _, id := range ids {
		for _, item := range s.byID[id] {
			task := s.tasks[item.task]
			records = append(records, TagProbeHistoryRecord{task.id, operationReport(cloneProbeOperation(task.operations[item.operation]), task.id)})
		}
	}
	return records
}

func (s *TagProbeSnapshot) Records() []TagProbeHistoryRecord {
	var records []TagProbeHistoryRecord
	for _, task := range s.tasks {
		for _, operation := range task.operations {
			records = append(records, TagProbeHistoryRecord{task.id, operationReport(cloneProbeOperation(operation), task.id)})
		}
	}
	return records
}

func cloneProbeOperation(operation domain.TagOperation) domain.TagOperation {
	copyString := func(value *string) *string {
		if value == nil {
			return nil
		}
		copy := *value
		return &copy
	}
	operation.TargetBranch = copyString(operation.TargetBranch)
	operation.SourceSHA, operation.TargetSHA = copyString(operation.SourceSHA), copyString(operation.TargetSHA)
	operation.Tag, operation.Message, operation.BatchID = copyString(operation.Tag), copyString(operation.Message), copyString(operation.BatchID)
	operation.ConflictFiles = append([]string{}, operation.ConflictFiles...)
	operation.GenbuStatus.BuiltCompletedAt = copyString(operation.GenbuStatus.BuiltCompletedAt)
	operation.GenbuStatus.ReleasedCompletedAt = copyString(operation.GenbuStatus.ReleasedCompletedAt)
	operation.GenbuStatus.ProductionReleasedCompletedAt = copyString(operation.GenbuStatus.ProductionReleasedCompletedAt)
	operation.GenbuStatus.CheckedAt, operation.GenbuStatus.FailureReason = copyString(operation.GenbuStatus.CheckedAt), copyString(operation.GenbuStatus.FailureReason)
	return operation
}

func WithTagProbeSnapshot(ctx context.Context, snapshot *TagProbeSnapshot) context.Context {
	return context.WithValue(ctx, tagProbeSnapshotKey{}, snapshot)
}

func (s *Service) probeSnapshot(ctx context.Context, config domain.AppConfig, tasks []domain.TaskManifest) (*TagProbeSnapshot, error) {
	if snapshot, ok := ctx.Value(tagProbeSnapshotKey{}).(*TagProbeSnapshot); ok && snapshot != nil {
		if snapshot.scope != tagProbeSnapshotScope(config, tasks) {
			return nil, errors.New("Tag 探测候选快照所属任务或配置已变化")
		}
		return snapshot, nil
	}
	return s.ReadTagProbeSnapshot(ctx, config, tasks)
}

// ReadTagProbeRecords refreshes only the selected operations after a status
// poll. It rereads task identity and never probes or recreates missing records.
func (s *Service) ReadTagProbeRecords(ctx context.Context, config domain.AppConfig, tasks []domain.TaskManifest, ids []string) ([]TagProbeHistoryRecord, error) {
	var records []TagProbeHistoryRecord
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return records, err
		}
		entry, err := s.manifests.LoadTask(value(config.TaskRoot), task.TaskDirectoryName)
		if err != nil {
			return records, err
		}
		if entry.Manifest.CreatedAt != task.CreatedAt {
			return records, errors.New("Tag 探测所属任务已重新创建")
		}
		for _, id := range ids {
			operation, err := s.store.load(entry.Path, id)
			if err != nil {
				return records, err
			}
			records = append(records, TagProbeHistoryRecord{task.TaskDirectoryName, operationReport(operation, task.TaskDirectoryName)})
		}
	}
	return records, nil
}
