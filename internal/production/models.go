// Package production owns service-scoped release batches. It deliberately does
// not use task manifests or the test-Tag version policy.
package production

import "silverwing-go/internal/domain"

type Selection struct {
	GroupID   string `json:"groupId"`
	ServiceID string `json:"serviceId"`
}

// Target is an immutable confirmation scope. Identities are digests: repository
// URLs and environment credentials must never be written into release history.
type Target struct {
	Selection
	ServiceName     string `json:"serviceName"`
	RepositoryID    string `json:"repositoryId"`
	Repository      string `json:"repository"`
	CommonDirectory string `json:"commonDirectory"`
	Remote          string `json:"remote"`
	MainBranch      string `json:"mainBranch"`
	RemoteIdentity  string `json:"remoteIdentity"`
	ConfigIdentity  string `json:"configIdentity"`
	GenbuService    string `json:"genbuService"`
	IDE             string `json:"ide"`
}

type Preview struct {
	Target               Target                     `json:"target"`
	ObservedAt           string                     `json:"observedAt"`
	ProductionCandidates []string                   `json:"productionCandidates"`
	RuntimeAvailable     bool                       `json:"runtimeAvailable"`
	ProductionTag        string                     `json:"productionTag"`
	ProductionSHA        string                     `json:"productionSha"`
	ProductionInMain     bool                       `json:"productionInMain,omitempty"`
	MainInRelease        bool                       `json:"mainInRelease,omitempty"`
	ProductionStatus     domain.GenbuTagProbeStatus `json:"productionStatus"`
	Manual               bool                       `json:"manual"`
	Warning              string                     `json:"warning"`
	MainSHA              string                     `json:"mainSha"`
	ReleaseSHA           string                     `json:"releaseSha"`
	SuggestedVersion     string                     `json:"suggestedVersion"`
	Branches             []string                   `json:"branches"`
	BranchHeads          map[string]string          `json:"branchHeads"`
	Tags                 []string                   `json:"tags"`
	Commits              string                     `json:"commits"`
	Diff                 string                     `json:"diff"`
}

type Merge struct {
	ID           string   `json:"id"`
	TargetBranch string   `json:"targetBranch"`
	SourceSHA    string   `json:"sourceSha"`
	BeforeSHA    string   `json:"beforeSha"`
	AfterSHA     string   `json:"afterSha,omitempty"`
	Worktree     string   `json:"worktree"`
	Branch       string   `json:"branch"`
	NoFF         bool     `json:"noFF"`
	Conflicts    []string `json:"conflicts,omitempty"`
}

type Intent struct {
	ResultConfirmed bool    `json:"resultConfirmed,omitempty"`
	Kind            string  `json:"kind"`
	Step            string  `json:"step"`
	Preview         Preview `json:"preview"`
	SourceBranch    string  `json:"sourceBranch,omitempty"`
	SourceSHA       string  `json:"sourceSha,omitempty"`
	Merge           *Merge  `json:"merge,omitempty"`
	Tag             string  `json:"tag,omitempty"`
	TagCommit       string  `json:"tagCommit,omitempty"`
	TagObject       string  `json:"tagObject,omitempty"`
}

type MergedBranch struct {
	Branch    string `json:"branch"`
	SourceSHA string `json:"sourceSha"`
	Commit    string `json:"commit"`
	At        string `json:"at"`
}

type Tag struct {
	Name     string                     `json:"name"`
	Commit   string                     `json:"commit"`
	Object   string                     `json:"object"`
	PushedAt string                     `json:"pushedAt"`
	Status   domain.GenbuTagProbeStatus `json:"status"`
}

type TagPlan struct {
	Token   string  `json:"token"`
	Tag     string  `json:"tag"`
	Commit  string  `json:"commit"`
	MainSHA string  `json:"mainSha"`
	Preview Preview `json:"preview"`
	Diff    string  `json:"diff"`
	Commits string  `json:"commits"`
}

type Batch struct {
	Schema            int            `json:"schema"`
	ID                string         `json:"id"`
	Target            Target         `json:"target"`
	Version           string         `json:"version"`
	ReleaseBranch     string         `json:"releaseBranch"`
	CreatedAt         string         `json:"createdAt"`
	UpdatedAt         string         `json:"updatedAt"`
	Ended             bool           `json:"ended"`
	Phase             string         `json:"phase"`
	Message           string         `json:"message"`
	MainSHA           string         `json:"mainSha"`
	InitialSHA        string         `json:"initialSha"`
	ReleaseSHA        string         `json:"releaseSha"`
	ProductionTag     string         `json:"productionTag"`
	ProductionSHA     string         `json:"productionSha"`
	Branches          []MergedBranch `json:"branches"`
	Tags              []Tag          `json:"tags"`
	Intent            *Intent        `json:"intent,omitempty"`
	TagPlan           *TagPlan       `json:"tagPlan,omitempty"`
	RetainedWorktrees []string       `json:"retainedWorktrees,omitempty"`
}

func (b Batch) LastTagCommit() string {
	if len(b.Tags) > 0 {
		return b.Tags[len(b.Tags)-1].Commit
	}
	return b.InitialSHA
}

type Report struct {
	Batches []Batch  `json:"batches"`
	Preview *Preview `json:"preview,omitempty"`
	Batch   *Batch   `json:"batch,omitempty"`
}
