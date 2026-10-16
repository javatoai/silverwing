package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type FeishuWikiNode struct {
	SpaceID         string `json:"spaceId"`
	NodeToken       string `json:"nodeToken"`
	ObjectToken     string `json:"objectToken"`
	ObjectType      string `json:"objectType"`
	ParentNodeToken string `json:"parentNodeToken"`
	Title           string `json:"title"`
}

type FeishuDocumentMetadata struct {
	Revision int64  `json:"revision"`
	Title    string `json:"title"`
}
type FeishuTextDocument struct {
	Content             string `json:"content"`
	Revision            int64  `json:"revision"`
	CommentsFingerprint string `json:"commentsFingerprint"`
}
type FeishuFileFingerprint struct {
	SHA256        string              `json:"sha256"`
	RemoteVersion FeishuRemoteVersion `json:"remoteVersion"`
}
type FeishuRemoteVersion struct {
	Version             string `json:"version"`
	CommentsFingerprint string `json:"commentsFingerprint"`
}

type FeishuUnconfirmedCreationError struct {
	Message   string
	ErrorCode *int
	LogID     *string
	Cause     error
}

func (e *FeishuUnconfirmedCreationError) Error() string { return e.Message }
func (e *FeishuUnconfirmedCreationError) Unwrap() error { return e.Cause }

type FeishuWriteResult struct {
	Node                     FeishuWikiNode
	Version                  *string
	CommentsFingerprint      *string
	VerifiedDocument         *FeishuTextDocument
	UnchangedBodyFromVersion *string
}

type MaterialsSyncCommandMetric struct {
	Operation      string `json:"operation"`
	DurationMillis int64  `json:"durationMillis"`
	Retries        int    `json:"retries"`
}
type MaterialsSyncCommandRetry struct {
	Operation     string `json:"operation"`
	RetryNumber   int    `json:"retryNumber"`
	MaxRetries    int    `json:"maxRetries"`
	DelayMillis   int64  `json:"delayMillis"`
	ErrorCode     *int   `json:"errorCode"`
	Path          string `json:"path"`
	ID            string `json:"id"`
	RetryAtMillis int64  `json:"retryAtMillis"`
}

func NewMaterialsSyncCommandRetry(operation string, retryNumber, maxRetries int, delayMillis int64, errorCode *int, path string, now time.Time) (MaterialsSyncCommandRetry, error) {
	if maxRetries == 0 {
		maxRetries = 8
	}
	if delayMillis < 0 {
		delayMillis = 0
	}
	if delayMillis > int64(^uint64(0)>>1)-now.UnixMilli() {
		delayMillis = int64(^uint64(0)>>1) - now.UnixMilli()
	}
	id, err := newID()
	if err != nil {
		return MaterialsSyncCommandRetry{}, err
	}
	return MaterialsSyncCommandRetry{Operation: operation, RetryNumber: retryNumber, MaxRetries: maxRetries, DelayMillis: delayMillis, ErrorCode: errorCode, Path: path, ID: id, RetryAtMillis: now.UnixMilli() + delayMillis}, nil
}

func newID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

// FeishuMaterialsGateway lists the required operations. Optional capabilities
// from the Kotlin interface are separate interfaces so implementations can opt in.
type FeishuMaterialsGateway interface {
	ResolveTarget(url string) (FeishuWikiNode, error)
	GetNode(nodeToken string) (*FeishuWikiNode, error)
	ListChildren(parent FeishuWikiNode) ([]FeishuWikiNode, error)
	DocumentMetadata(node FeishuWikiNode) (FeishuDocumentMetadata, error)
	ReadDocument(node FeishuWikiNode, includeComments bool) (FeishuTextDocument, error)
	Version(node FeishuWikiNode, includeComments bool) (FeishuRemoteVersion, error)
	CommentsFingerprint(node FeishuWikiNode) (string, error)
	CreateDirectory(parent FeishuWikiNode, title string) (FeishuWriteResult, error)
	CreateDocument(parent FeishuWikiNode, title, markdown, workingDirectory string, includeComments bool) (FeishuWriteResult, error)
	OverwriteDocument(node FeishuWikiNode, markdown string, expectedRevision int64, workingDirectory string, expectedCommentsFingerprint *string, expectedParent *FeishuWikiNode) (FeishuWriteResult, error)
	UploadFile(parent FeishuWikiNode, workingDirectory, relativePath, title string, existing *FeishuWikiNode, expectedVersion *FeishuRemoteVersion) (FeishuWriteResult, error)
	Rename(node FeishuWikiNode, title string) (FeishuWriteResult, error)
	DeleteNode(node FeishuWikiNode) error
}

type FeishuFileFingerprintReader interface {
	ReadFileFingerprint(node FeishuWikiNode) (FeishuFileFingerprint, error)
}
type FeishuCompleteDocumentCreator interface {
	SupportsCompleteDocumentCreation(markdown string) bool
	CreateCompleteDocument(parent FeishuWikiNode, title, markdown, workingDirectory string) (FeishuWriteResult, error)
}
type FeishuDocumentReservation interface {
	ReserveDocument(parent FeishuWikiNode, title, workingDirectory string) (FeishuWriteResult, error)
	CompleteReservedDocument(node FeishuWikiNode, title, markdown string, expectedVersion FeishuRemoteVersion, workingDirectory string, parent FeishuWikiNode, onWritten func(FeishuWriteResult)) (FeishuWriteResult, error)
}

type MaterialsSyncKind string

const (
	MaterialsSyncDirectory MaterialsSyncKind = "DIRECTORY"
	MaterialsSyncMarkdown  MaterialsSyncKind = "MARKDOWN"
	MaterialsSyncFile      MaterialsSyncKind = "FILE"
)

type MaterialsSyncEntry struct {
	Path            string              `json:"path"`
	Kind            MaterialsSyncKind   `json:"kind"`
	NodeToken       string              `json:"nodeToken"`
	ObjectToken     string              `json:"objectToken"`
	ObjectType      string              `json:"objectType"`
	ParentNodeToken string              `json:"parentNodeToken"`
	Title           string              `json:"title"`
	LocalHash       string              `json:"localHash"`
	RemoteVersion   FeishuRemoteVersion `json:"remoteVersion"`
}

type MaterialsSyncIntent struct {
	OperationID         string               `json:"operationId"`
	Path                string               `json:"path"`
	Kind                MaterialsSyncKind    `json:"kind"`
	ParentNodeToken     string               `json:"parentNodeToken"`
	Title               string               `json:"title"`
	TemporaryTitle      string               `json:"temporaryTitle"`
	LocalHash           string               `json:"localHash"`
	Phase               string               `json:"phase"`
	NodeToken           *string              `json:"nodeToken"`
	BaseVersion         *FeishuRemoteVersion `json:"baseVersion"`
	ExpectedContentHash *string              `json:"expectedContentHash"`
	Placeholder         bool                 `json:"placeholder"`
}

type MaterialsSyncBinding struct {
	SourceID         string                        `json:"sourceId"`
	RequirementID    string                        `json:"requirementId"`
	RequirementTitle string                        `json:"requirementTitle"`
	Subdirectory     string                        `json:"subdirectory"`
	Entries          map[string]MaterialsSyncEntry `json:"entries"`
	Pending          []MaterialsSyncIntent         `json:"pending"`
}

type MaterialsSyncState string

const (
	MaterialsSyncInitializing MaterialsSyncState = "INITIALIZING"
	MaterialsSyncReady        MaterialsSyncState = "READY"
)

type MaterialsSyncManifest struct {
	FormatVersion        int                             `json:"formatVersion"`
	Manager              string                          `json:"manager"`
	Namespace            string                          `json:"namespace"`
	TargetNodeToken      string                          `json:"targetNodeToken"`
	SpaceID              string                          `json:"spaceId"`
	ScopeID              string                          `json:"scopeId"`
	RequirementNodeToken string                          `json:"requirementNodeToken"`
	State                MaterialsSyncState              `json:"state"`
	OperationID          string                          `json:"operationId"`
	Generation           int64                           `json:"generation"`
	Bindings             map[string]MaterialsSyncBinding `json:"bindings"`
}

type MaterialsSyncRequest struct {
	TargetURL        string   `json:"targetUrl"`
	WriteRoot        string   `json:"writeRoot"`
	RequirementID    string   `json:"requirementId"`
	RequirementTitle string   `json:"requirementTitle"`
	Subdirectory     string   `json:"subdirectory"`
	SelectedPaths    []string `json:"selectedPaths,omitempty"`
}
type MaterialsSyncAction string

const (
	MaterialsSyncCreate   MaterialsSyncAction = "CREATE"
	MaterialsSyncUpdate   MaterialsSyncAction = "UPDATE"
	MaterialsSyncDelete   MaterialsSyncAction = "DELETE"
	MaterialsSyncSkip     MaterialsSyncAction = "SKIP"
	MaterialsSyncConflict MaterialsSyncAction = "CONFLICT"
)

type MaterialsSyncItem struct {
	Path            string               `json:"path"`
	Kind            MaterialsSyncKind    `json:"kind"`
	Action          MaterialsSyncAction  `json:"action"`
	LocalHash       string               `json:"localHash"`
	Reason          *string              `json:"reason"`
	Entry           *MaterialsSyncEntry  `json:"entry"`
	ObservedVersion *FeishuRemoteVersion `json:"observedVersion"`
	ObservedNode    *FeishuWikiNode      `json:"observedNode"`
}

type MaterialsSyncDiscoverySnapshot struct {
	TargetNodeToken string                           `json:"targetNodeToken"`
	SpaceID         string                           `json:"spaceId"`
	ScopeID         string                           `json:"scopeId"`
	TargetChildren  []FeishuWikiNode                 `json:"targetChildren"`
	Managed         []MaterialsSyncDiscoveryManifest `json:"managed"`
	Bootstrap       *MaterialsBootstrapRecovery      `json:"bootstrap,omitempty"`
}

// MaterialsBootstrapRecovery is a Go-owned checkpoint for one root creation.
// Including it in discovery binds approval to the same recovery operation.
type MaterialsBootstrapRecovery struct {
	ScopeID        string                 `json:"scopeId"`
	OperationID    string                 `json:"operationId"`
	CandidateTitle string                 `json:"candidateTitle"`
	Phase          string                 `json:"phase"`
	Root           *FeishuWikiNode        `json:"root,omitempty"`
	Manifest       *MaterialsSyncManifest `json:"manifest,omitempty"`
	Error          string                 `json:"error,omitempty"`
}

type MaterialsSyncDiscoveryManifest struct {
	Node            FeishuWikiNode        `json:"node"`
	Revision        int64                 `json:"revision"`
	Value           MaterialsSyncManifest `json:"value"`
	RequirementRoot FeishuWikiNode        `json:"requirementRoot"`
}
type MaterialsSyncPlan struct {
	ID                string                          `json:"id"`
	Request           MaterialsSyncRequest            `json:"request"`
	SelectedPaths     []string                        `json:"selectedPaths,omitempty"`
	Target            FeishuWikiNode                  `json:"target"`
	SourceID          string                          `json:"sourceId"`
	ManifestNode      *FeishuWikiNode                 `json:"manifestNode"`
	ManifestRevision  *int64                          `json:"manifestRevision"`
	Manifest          *MaterialsSyncManifest          `json:"manifest"`
	Items             []MaterialsSyncItem             `json:"items"`
	ScanFingerprint   string                          `json:"scanFingerprint"`
	ScopeID           string                          `json:"scopeId"`
	RequirementRoot   *FeishuWikiNode                 `json:"requirementRoot"`
	DiscoverySnapshot *MaterialsSyncDiscoverySnapshot `json:"discoverySnapshot"`
}

type MaterialsSyncApproval struct {
	PlanID         string              `json:"planId"`
	DeletionPaths  map[string]struct{} `json:"deletionPaths"`
	OverwritePaths map[string]struct{} `json:"overwritePaths"`
}
type MaterialsSyncPhase string

const (
	MaterialsSyncScan              MaterialsSyncPhase = "SCAN"
	MaterialsSyncVerify            MaterialsSyncPhase = "VERIFY"
	MaterialsSyncInitialize        MaterialsSyncPhase = "INITIALIZE"
	MaterialsSyncCreateDirectories MaterialsSyncPhase = "CREATE_DIRECTORIES"
	MaterialsSyncReserveNodes      MaterialsSyncPhase = "RESERVE_NODES"
	MaterialsSyncUpload            MaterialsSyncPhase = "UPLOAD"
	MaterialsSyncSaveRecords       MaterialsSyncPhase = "SAVE_RECORDS"
	MaterialsSyncDeletePhase       MaterialsSyncPhase = "DELETE"
	MaterialsSyncComplete          MaterialsSyncPhase = "COMPLETE"
)

type MaterialsSyncProgress struct {
	Added          int                         `json:"added"`
	Updated        int                         `json:"updated"`
	Deleted        int                         `json:"deleted"`
	Skipped        int                         `json:"skipped"`
	Conflicts      int                         `json:"conflicts"`
	Failed         int                         `json:"failed"`
	CurrentPath    string                      `json:"currentPath"`
	Phase          MaterialsSyncPhase          `json:"phase"`
	Directories    int                         `json:"directories"`
	CompletedFiles int                         `json:"completedFiles"`
	TotalFiles     int                         `json:"totalFiles"`
	ActivePaths    []string                    `json:"activePaths"`
	Sequence       int64                       `json:"sequence"`
	CurrentDepth   int                         `json:"currentDepth"`
	RetryCount     int                         `json:"retryCount"`
	WaitingRetries []MaterialsSyncCommandRetry `json:"waitingRetries"`
}

type MaterialsSyncResult struct {
	Progress  MaterialsSyncProgress `json:"progress"`
	Messages  []string              `json:"messages"`
	Cancelled bool                  `json:"cancelled"`
}

func (r MaterialsSyncResult) Partial() bool {
	return r.Cancelled || r.Progress.Failed > 0 || r.Progress.Conflicts > 0
}

func ValidateMaterialsSyncManager(manifest MaterialsSyncManifest) error {
	if manifest.FormatVersion != 2 || manifest.Manager != "SilverWing" {
		return fmt.Errorf("unsupported materials sync manifest format")
	}
	return nil
}
