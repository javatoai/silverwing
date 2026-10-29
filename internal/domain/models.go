package domain

import "silverwing-go/internal/productprofile"

// Persisted schema versions are deliberately independent of the product version.
const (
	CurrentProductVersion            = "0.0.7"
	CurrentAppConfigSchemaVersion    = "6.0.0"
	CurrentTaskManifestSchemaVersion = "2.0.0"
	DefaultGroupID                   = "default"
	DefaultGroupName                 = "默认组"
)

type ThemePreference string

const (
	ThemeSystem ThemePreference = "SYSTEM"
	ThemeLight  ThemePreference = "LIGHT"
	ThemeDark   ThemePreference = "DARK"
)

type CommandProxyTarget string

const (
	ProxyTargetCodex CommandProxyTarget = "CODEX"
	ProxyTargetGit   CommandProxyTarget = "GIT"

	ProxyTargetMeegle CommandProxyTarget = "MEEGLE"
)

// NormalizeCommandProxyTargets applies the persisted Set semantics while
// preserving the first-seen order used by Kotlin serialization.
func NormalizeCommandProxyTargets(values []CommandProxyTarget) []CommandProxyTarget {
	seen := make(map[CommandProxyTarget]struct{}, len(values))
	out := make([]CommandProxyTarget, 0, len(values))
	for _, value := range values {
		// Lark cloud-document synchronization has been retired. Ignore the old
		// routing entry while loading otherwise valid configurations.
		if value == CommandProxyTarget("LARK") {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// EqualCommandProxyTargetSets compares the command proxy targets as a Set.
func EqualCommandProxyTargetSets(left, right []CommandProxyTarget) bool {
	leftSet := make(map[CommandProxyTarget]struct{}, len(left))
	for _, value := range left {
		leftSet[value] = struct{}{}
	}
	rightSet := make(map[CommandProxyTarget]struct{}, len(right))
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	if len(leftSet) != len(rightSet) {
		return false
	}
	for value := range rightSet {
		if _, exists := leftSet[value]; !exists {
			return false
		}
	}
	return true
}

type MeegleProjectConfig struct {
	ProjectKey string `json:"project_key"`
	SimpleName string `json:"simple_name"`
}
type CodexPluginMarketplaceSource struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	RepositoryURL        string  `json:"repositoryUrl"`
	Ref                  *string `json:"ref"`
	MarketplaceDirectory string  `json:"marketplaceDirectory"`
}
type SkillSource struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	RepositoryURL string  `json:"repositoryUrl"`
	Ref           *string `json:"ref"`
	SkillRoot     *string `json:"skillRoot"`
}

type GroupConfig struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ToolVisibilitySettings struct {
	JSON     bool `json:"json"`
	DateTime bool `json:"dateTime"`
	TextDiff bool `json:"textDiff"`
	KillPort bool `json:"killPort"`
}
type SidebarVisibilitySettings struct {
	Requirements bool `json:"requirements"`
	Tools        bool `json:"tools"`
}
type AppConfig struct {
	Edition       string  `json:"edition"`
	SchemaVersion string  `json:"schemaVersion"`
	TaskRoot      *string `json:"taskRoot"`

	Groups            []GroupConfig             `json:"groups"`
	Theme             ThemePreference           `json:"theme"`
	ToolVisibility    ToolVisibilitySettings    `json:"toolVisibility"`
	SidebarVisibility SidebarVisibilitySettings `json:"sidebarVisibility"`

	TerminalExecutable *string `json:"terminalExecutable"`

	DefaultWorkspaceToolIDs []string `json:"defaultWorkspaceToolIds"`

	MeegleProjects                []MeegleProjectConfig `json:"meegleProjects"`
	MeegleDefaultSprintProjectKey *string               `json:"meegleDefaultSprintProjectKey"`
	MeegleExecutablePath          *string               `json:"meegleExecutablePath"`
	CodexExecutablePath           *string               `json:"codexExecutablePath"`
	CommandProxyURL               *string               `json:"commandProxyUrl"`
	CommandProxyNoProxy           *string               `json:"commandProxyNoProxy"`
	CommandProxyUsername          *string               `json:"commandProxyUsername"`
	CommandProxyPassword          *string               `json:"commandProxyPassword"`
	CommandProxyTargets           []CommandProxyTarget  `json:"commandProxyTargets"`
	GitExecutablePath             *string               `json:"gitExecutablePath"`

	RequirementMaterialsRoot          *string                        `json:"requirementMaterialsRoot"`
	RequirementMaterialsSubdirectory  *string                        `json:"requirementMaterialsSubdirectory"`
	MaterialsGitSync                  *MaterialsGitSyncSettings      `json:"materialsGitSync"`
	CodexPluginMarketplaceSources     []CodexPluginMarketplaceSource `json:"codexPluginMarketplaceSources"`
	SkillSources                      []SkillSource                  `json:"skillSources"`
	AIRequirementNamingEnabled        bool                           `json:"aiRequirementNamingEnabled"`
	AIRequirementNamingPrewarmEnabled bool                           `json:"aiRequirementNamingPrewarmEnabled"`
	AIRequirementNamingModel          string                         `json:"aiRequirementNamingModel"`
}

type TaskLifecycleStatus string

const (
	TaskActive   TaskLifecycleStatus = "ACTIVE"
	TaskArchived TaskLifecycleStatus = "ARCHIVED"
)

type RequirementMaterialsStatus string

const (
	MaterialsNotRequested RequirementMaterialsStatus = "NOT_REQUESTED"
	MaterialsReady        RequirementMaterialsStatus = "READY"
	MaterialsFailed       RequirementMaterialsStatus = "FAILED"
)

type RequirementMaterialsDirectory struct {
	Status        RequirementMaterialsStatus `json:"status"`
	WriteRoot     *string                    `json:"writeRoot"`
	FailureReason *string                    `json:"failureReason"`
}

type AgentDocumentLayout string

const (
	AgentDocumentInlineV1     AgentDocumentLayout = "INLINE_V1"
	AgentDocumentReferencedV2 AgentDocumentLayout = "REFERENCED_V2"
)

type TaskManifest struct {
	Edition           string `json:"edition"`
	SchemaVersion     string `json:"schemaVersion"`
	FolderName        string `json:"folderName"`
	TaskDirectoryName string `json:"taskDirectoryName"`

	RequirementLink      string                        `json:"requirementLink"`
	RequirementID        *string                       `json:"requirementId"`
	RequirementMaterials RequirementMaterialsDirectory `json:"requirementMaterials"`
	CreatedAt            string                        `json:"createdAt"`
	UpdatedAt            string                        `json:"updatedAt"`
	LifecycleStatus      TaskLifecycleStatus           `json:"lifecycleStatus"`

	GroupID               string                `json:"groupId"`
	WorkspaceToolLaunches []WorkspaceToolLaunch `json:"workspaceToolLaunches"`
	AgentContext          *AgentTaskContext     `json:"agentContext"`
	AgentDocumentLayout   AgentDocumentLayout   `json:"agentDocumentLayout"`
}
type AgentTaskContext struct {
	ProtocolVersion        string `json:"protocolVersion"`
	DocumentationDirectory string `json:"documentationDirectory"`
	HandoffRelativePath    string `json:"handoffRelativePath"`
}
type WorkspaceToolLaunchStatus string

const (
	WorkspaceToolPending WorkspaceToolLaunchStatus = "PENDING"
	WorkspaceToolOpened  WorkspaceToolLaunchStatus = "OPENED"
	WorkspaceToolFailed  WorkspaceToolLaunchStatus = "FAILED"
)

type WorkspaceToolLaunch struct {
	ToolID    string                    `json:"toolId"`
	Status    WorkspaceToolLaunchStatus `json:"status"`
	UpdatedAt string                    `json:"updatedAt"`
	Message   *string                   `json:"message"`
}

type AgentTaskTemplate struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Content   string `json:"content"`
	UpdatedAt string `json:"updatedAt"`
}
type CommandResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type RemoteBranchRef struct {
	Remote string `json:"remote"`
	Branch string `json:"branch"`
}
type RequirementAiNamingSuggestion struct {
	FolderName string `json:"folderName"`
}

const DefaultCommandProxyURL = "http://127.0.0.1:7897"

func DefaultCommandProxyTargets(endpoint *string) []CommandProxyTarget {
	if endpoint != nil && *endpoint != "" {
		return []CommandProxyTarget{ProxyTargetCodex}
	}
	return []CommandProxyTarget{}
}

func DefaultAppConfig() AppConfig {
	endpoint := DefaultCommandProxyURL
	return AppConfig{Edition: productprofile.Edition, SchemaVersion: CurrentAppConfigSchemaVersion, Groups: []GroupConfig{{ID: DefaultGroupID, Name: DefaultGroupName}}, Theme: ThemeSystem, ToolVisibility: ToolVisibilitySettings{JSON: true, DateTime: true, TextDiff: true, KillPort: true}, SidebarVisibility: SidebarVisibilitySettings{Requirements: true, Tools: true}, DefaultWorkspaceToolIDs: []string{"codex"}, MeegleProjects: []MeegleProjectConfig{}, CommandProxyURL: &endpoint, CommandProxyTargets: DefaultCommandProxyTargets(&endpoint), CodexPluginMarketplaceSources: []CodexPluginMarketplaceSource{}, SkillSources: []SkillSource{}, AIRequirementNamingEnabled: true, AIRequirementNamingPrewarmEnabled: true, AIRequirementNamingModel: DefaultAIRequirementNamingModel}
}

const DefaultAIRequirementNamingModel = "gpt-6-luna"

func DefaultTaskManifest() TaskManifest {
	return TaskManifest{Edition: productprofile.Edition, SchemaVersion: CurrentTaskManifestSchemaVersion, RequirementLink: "", RequirementMaterials: RequirementMaterialsDirectory{Status: MaterialsNotRequested}, LifecycleStatus: TaskActive, GroupID: DefaultGroupID, WorkspaceToolLaunches: []WorkspaceToolLaunch{}, AgentDocumentLayout: AgentDocumentReferencedV2}
}
