package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// TagJobScope captures the caller's immutable task and account context. Account
// identity is the already confirmed opaque UI identity, not a new CLI probe.
type TagJobScope struct {
	Home, TaskRoot, TaskID, TaskPath, TaskCreatedAt string
	ConfigIdentity, AccountIdentity                 string
}

type TagJobPhase string

const (
	TagJobPhaseQueued        TagJobPhase = "queued"
	TagJobPhaseChecking      TagJobPhase = "checking"
	TagJobPhasePushingSource TagJobPhase = "pushing-source"
	TagJobPhaseMerging       TagJobPhase = "merging"
	TagJobPhasePushingTarget TagJobPhase = "pushing-target"
	TagJobPhaseCreatingTag   TagJobPhase = "creating-tag"
	TagJobPhasePushingTag    TagJobPhase = "pushing-tag"
	TagJobPhaseCompleted     TagJobPhase = "completed"
	TagJobPhaseFailed        TagJobPhase = "failed"
	TagJobPhaseConflict      TagJobPhase = "conflict"
	TagJobPhaseCanceled      TagJobPhase = "canceled"
)

const (
	TagJobFailureConflict    = "conflict"
	TagJobFailureUncommitted = "uncommitted"
	TagJobFailureOther       = "other"
	TagJobFailureCanceled    = "canceled"
)

type TagJobCheckResult struct {
	Clean   bool
	Changes []string
}

type TagJobItem struct {
	ID, Kind, SelectionKey, ServiceID, ModuleID string
	WorkspacePath, OperationID                  string
	Phase                                       TagJobPhase
	Step                                        string
	Busy, Queued, Done                          bool
	FailureKind, Error                          string
	Record                                      *TagRecord
	Check                                       *TagJobCheckResult
}

// Each notification is a complete snapshot. Sequence is monotonic within ID;
// a slow subscriber can skip superseded snapshots without losing an item.
type TagJobSnapshot struct {
	Scope            TagJobScope
	ID, BatchID      string
	Sequence         uint64
	Busy, Cancelling bool
	Items            []TagJobItem
	Error            string
}

type TagJobBackend interface {
	StartTagJob(context.Context, TagJobScope, Action) (TagJobSnapshot, error)
	AppendTagJob(context.Context, TagJobScope, string, Action) (TagJobSnapshot, error)
	TagJobSnapshot(context.Context, TagJobScope) (TagJobSnapshot, error)
	WatchTagJob(context.Context, TagJobScope, string, uint64, func(TagJobSnapshot)) error
	CancelTagJob(context.Context, TagJobScope, string) error
}

type TagJobCloser interface{ CloseTagJobs(context.Context) error }

// A probe emits each accepted persisted record immediately. Scheduling cadence
// belongs to the caller; the backend coalesces overlapping repository probes.
type TagProbeBackend interface {
	DispatchTagProbe(context.Context, Action, func(TagRecord)) (ActionResult, error)
}

// TagJobConfigIdentity compares equivalent JSON independently of indentation or
// property order. The digest never exposes account or integration settings.
func TagJobConfigIdentity(source string) string {
	var config any
	if json.Unmarshal([]byte(source), &config) != nil {
		return ""
	}
	canonical, err := json.Marshal(config)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:])
}
