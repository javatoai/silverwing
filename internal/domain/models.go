package domain

// Persisted schema versions are deliberately independent of the product version.
const (
	CurrentProductVersion            = "0.0.7"
	CurrentAppConfigSchemaVersion    = "6.0.0"
	CurrentTaskManifestSchemaVersion = "2.0.0"
	DefaultGroupID                   = "default"
	DefaultGroupName                 = "默认组"
	DefaultTagHistoryMaxGroups       = 3
	MaxTagHistoryGroups              = 1000
)

type DevelopmentToolType string

const (
	DevelopmentToolIntelliJIdea     DevelopmentToolType = "INTELLIJ_IDEA"
	DevelopmentToolWebStorm         DevelopmentToolType = "WEBSTORM"
	DevelopmentToolPyCharm          DevelopmentToolType = "PYCHARM"
	DevelopmentToolVisualStudioCode DevelopmentToolType = "VISUAL_STUDIO_CODE"
	DevelopmentToolAndroidStudio    DevelopmentToolType = "ANDROID_STUDIO"
	DevelopmentToolDevEcoStudio     DevelopmentToolType = "DEVECO_STUDIO"
)

type ThemePreference string

const (
	ThemeSystem ThemePreference = "SYSTEM"
	ThemeLight  ThemePreference = "LIGHT"
	ThemeDark   ThemePreference = "DARK"
)

type WorkspaceStrategy string

const (
	WorkspaceStandardWorktree WorkspaceStrategy = "STANDARD_WORKTREE"
	WorkspaceIndependentClone WorkspaceStrategy = "INDEPENDENT_CLONE"
)

type TagBuildMode string

const (
	TagBuildMergeToTargetBranch TagBuildMode = "MERGE_TO_TARGET_BRANCH"
	TagBuildCurrentBranch       TagBuildMode = "CURRENT_BRANCH"
)

type CommandProxyTarget string

const (
	ProxyTargetCodex            CommandProxyTarget = "CODEX"
	ProxyTargetGit              CommandProxyTarget = "GIT"
	ProxyTargetWorkspaceCommand CommandProxyTarget = "WORKSPACE_COMMAND"
	ProxyTargetMeegle           CommandProxyTarget = "MEEGLE"
	ProxyTargetLark             CommandProxyTarget = "LARK"
	ProxyTargetGenbu            CommandProxyTarget = "GENBU"
)

// NormalizeCommandProxyTargets applies the persisted Set semantics while
// preserving the first-seen order used by Kotlin serialization.
func NormalizeCommandProxyTargets(values []CommandProxyTarget) []CommandProxyTarget {
	seen := make(map[CommandProxyTarget]struct{}, len(values))
	out := make([]CommandProxyTarget, 0, len(values))
	for _, value := range values {
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

type DevelopmentToolConfig struct {
	Type DevelopmentToolType `json:"type"`
	Path string              `json:"path"`
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
type BootstrapCopyRule struct {
	Source    string `json:"source"`
	Target    string `json:"target"`
	Overwrite bool   `json:"overwrite"`
}
type BootstrapCommand struct {
	Name             string   `json:"name"`
	Executable       string   `json:"executable"`
	Arguments        []string `json:"arguments"`
	WorkingDirectory string   `json:"workingDirectory"`
	TimeoutSeconds   int64    `json:"timeoutSeconds"`
	Enabled          bool     `json:"enabled"`
}
type BootstrapConfig struct {
	CopyRules []BootstrapCopyRule `json:"copyRules"`
	Commands  []BootstrapCommand  `json:"commands"`
}
type WorkspaceCommandConfig struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	IconKey          string   `json:"iconKey"`
	Executable       string   `json:"executable"`
	Arguments        []string `json:"arguments"`
	WorkingDirectory string   `json:"workingDirectory"`
	TimeoutSeconds   int64    `json:"timeoutSeconds"`
	Enabled          bool     `json:"enabled"`
}
type RepositoryConfig struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	RootPath            string  `json:"rootPath"`
	GitCommonDirectory  string  `json:"gitCommonDirectory"`
	OriginURL           *string `json:"originUrl"`
	CurrentBranch       *string `json:"currentBranch"`
	DefaultRemoteBranch *string `json:"defaultRemoteBranch"`
}
type ServiceModuleConfig struct {
	ID               string                   `json:"id"`
	Name             string                   `json:"name"`
	MasterBranch     *string                  `json:"masterBranch"`
	Strategy         WorkspaceStrategy        `json:"strategy"`
	TagEnabled       bool                     `json:"tagEnabled"`
	TagMode          TagBuildMode             `json:"tagMode"`
	TagTargetRef     *string                  `json:"tagTargetRef"`
	TagMessagePrefix string                   `json:"tagMessagePrefix"`
	CustomCommands   []WorkspaceCommandConfig `json:"customCommands"`
}
type GroupServiceConfig struct {
	ID                    string                `json:"id"`
	RepositoryID          string                `json:"repositoryId"`
	DisplayName           string                `json:"displayName"`
	Enabled               bool                  `json:"enabled"`
	GenbuProbeEnabled     bool                  `json:"genbuProbeEnabled"`
	GenbuServiceName      string                `json:"genbuServiceName"`
	MasterBranch          string                `json:"masterBranch"`
	TestTagBaselineRef    string                `json:"testTagBaselineRef"`
	DevelopmentTool       DevelopmentToolType   `json:"developmentTool"`
	Modules               []ServiceModuleConfig `json:"modules"`
	Bootstrap             BootstrapConfig       `json:"bootstrap"`
	CommitMessageTemplate string                `json:"commitMessageTemplate"`
}
type GroupConfig struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	TagEnabled bool                 `json:"tagEnabled"`
	Services   []GroupServiceConfig `json:"services"`
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
	Production   bool `json:"production"`
	Services     bool `json:"services"`
}
type AppConfig struct {
	SchemaVersion                          string                         `json:"schemaVersion"`
	TaskRoot                               *string                        `json:"taskRoot"`
	DefaultBranchPrefix                    string                         `json:"defaultBranchPrefix"`
	Repositories                           []RepositoryConfig             `json:"repositories"`
	Groups                                 []GroupConfig                  `json:"groups"`
	Theme                                  ThemePreference                `json:"theme"`
	ToolVisibility                         ToolVisibilitySettings         `json:"toolVisibility"`
	SidebarVisibility                      SidebarVisibilitySettings      `json:"sidebarVisibility"`
	TagEnabled                             bool                           `json:"tagEnabled"`
	TagHistoryMaxGroups                    int                            `json:"tagHistoryMaxGroups"`
	AllowTaskTagTargetEditing              bool                           `json:"allowTaskTagTargetEditing"`
	TerminalExecutable                     *string                        `json:"terminalExecutable"`
	DevelopmentTools                       []DevelopmentToolConfig        `json:"developmentTools"`
	DefaultDevelopmentTool                 DevelopmentToolType            `json:"defaultDevelopmentTool"`
	AllowTemporaryDevelopmentToolSelection bool                           `json:"allowTemporaryDevelopmentToolSelection"`
	DefaultWorkspaceToolIDs                []string                       `json:"defaultWorkspaceToolIds"`
	ShowTaskDetailGitActionGroup           bool                           `json:"showTaskDetailGitActionGroup"`
	ShowTaskDetailPathActionGroup          bool                           `json:"showTaskDetailPathActionGroup"`
	ShowWorkspaceGitActionGroup            bool                           `json:"showWorkspaceGitActionGroup"`
	ShowWorkspacePathActionGroup           bool                           `json:"showWorkspacePathActionGroup"`
	ShowTaskAreaCopyIcons                  bool                           `json:"showTaskAreaCopyIcons"`
	ShowTaskAreaBranchCopyIcons            bool                           `json:"showTaskAreaBranchCopyIcons"`
	ShowTaskAreaRequirementCopyIcons       bool                           `json:"showTaskAreaRequirementCopyIcons"`
	ShowTaskAreaProjectNameCopyIcons       bool                           `json:"showTaskAreaProjectNameCopyIcons"`
	BlockedGitWriteBranches                []string                       `json:"blockedGitWriteBranches"`
	MeegleProjects                         []MeegleProjectConfig          `json:"meegleProjects"`
	MeegleDefaultSprintProjectKey          *string                        `json:"meegleDefaultSprintProjectKey"`
	MeegleExecutablePath                   *string                        `json:"meegleExecutablePath"`
	CodexExecutablePath                    *string                        `json:"codexExecutablePath"`
	CommandProxyURL                        *string                        `json:"commandProxyUrl"`
	CommandProxyNoProxy                    *string                        `json:"commandProxyNoProxy"`
	CommandProxyUsername                   *string                        `json:"commandProxyUsername"`
	CommandProxyPassword                   *string                        `json:"commandProxyPassword"`
	CommandProxyTargets                    []CommandProxyTarget           `json:"commandProxyTargets"`
	GitExecutablePath                      *string                        `json:"gitExecutablePath"`
	GenbuExecutablePath                    *string                        `json:"genbuExecutablePath"`
	GenbuExecutableAutoDetected            bool                           `json:"genbuExecutableAutoDetected"`
	LarkExecutablePath                     *string                        `json:"larkExecutablePath"`
	RequirementMaterialsRoot               *string                        `json:"requirementMaterialsRoot"`
	RequirementMaterialsSubdirectory       *string                        `json:"requirementMaterialsSubdirectory"`
	RequirementMaterialsFeishuTarget       *string                        `json:"requirementMaterialsFeishuTarget"`
	CodexPluginMarketplaceSources          []CodexPluginMarketplaceSource `json:"codexPluginMarketplaceSources"`
	SkillSources                           []SkillSource                  `json:"skillSources"`
	AIRequirementNamingEnabled             bool                           `json:"aiRequirementNamingEnabled"`
	AIRequirementNamingPrewarmEnabled      bool                           `json:"aiRequirementNamingPrewarmEnabled"`
	AIRequirementNamingModel               string                         `json:"aiRequirementNamingModel"`
}

type RepositoryInfo struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	RootPath           string  `json:"rootPath"`
	GitCommonDirectory string  `json:"gitCommonDirectory"`
	RemoteURL          *string `json:"remoteUrl"`
	CurrentBranch      *string `json:"currentBranch"`
	IsBare             bool    `json:"isBare"`
}
type TaskLifecycleStatus string

const (
	TaskActive   TaskLifecycleStatus = "ACTIVE"
	TaskArchived TaskLifecycleStatus = "ARCHIVED"
)

type WorkspaceHealth string

const (
	WorkspaceCreating          WorkspaceHealth = "CREATING"
	WorkspaceReady             WorkspaceHealth = "READY"
	WorkspaceReadyWithWarnings WorkspaceHealth = "READY_WITH_WARNINGS"
	WorkspaceFailed            WorkspaceHealth = "FAILED"
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
type TaskModuleSource string

const (
	TaskModuleConfigured TaskModuleSource = "CONFIGURED"
	TaskModuleTemporary  TaskModuleSource = "TEMPORARY"
)

type ServiceWorkspace struct {
	RepositoryID        string              `json:"repositoryId"`
	ServiceName         string              `json:"serviceName"`
	RepositoryPath      string              `json:"repositoryPath"`
	WorktreePath        string              `json:"worktreePath"`
	DevelopmentTool     DevelopmentToolType `json:"developmentTool"`
	Branch              string              `json:"branch"`
	Health              WorkspaceHealth     `json:"health"`
	Warnings            []string            `json:"warnings"`
	GroupServiceID      string              `json:"groupServiceId"`
	ModuleID            string              `json:"moduleId"`
	ModuleName          string              `json:"moduleName"`
	Strategy            WorkspaceStrategy   `json:"strategy"`
	ModuleSource        TaskModuleSource    `json:"moduleSource"`
	OriginURL           *string             `json:"originUrl"`
	BaseRef             *string             `json:"baseRef"`
	TargetBranch        *string             `json:"targetBranch"`
	TagEnabled          bool                `json:"tagEnabled"`
	TagMode             TagBuildMode        `json:"tagMode"`
	TagTargetRef        *string             `json:"tagTargetRef"`
	TagMessagePrefix    string              `json:"tagMessagePrefix"`
	BranchCreatedByTask bool                `json:"branchCreatedByTask"`
	ForceWorktreeAttach bool                `json:"forceWorktreeAttach"`
	PushRemote          string              `json:"pushRemote"`
}
type AgentDocumentLayout string

const (
	AgentDocumentInlineV1     AgentDocumentLayout = "INLINE_V1"
	AgentDocumentReferencedV2 AgentDocumentLayout = "REFERENCED_V2"
)

type TaskManifest struct {
	SchemaVersion         string                        `json:"schemaVersion"`
	FolderName            string                        `json:"folderName"`
	TaskDirectoryName     string                        `json:"taskDirectoryName"`
	FeatureBranch         string                        `json:"featureBranch"`
	RequirementLink       string                        `json:"requirementLink"`
	RequirementID         *string                       `json:"requirementId"`
	RequirementMaterials  RequirementMaterialsDirectory `json:"requirementMaterials"`
	CreatedAt             string                        `json:"createdAt"`
	UpdatedAt             string                        `json:"updatedAt"`
	LifecycleStatus       TaskLifecycleStatus           `json:"lifecycleStatus"`
	Services              []ServiceWorkspace            `json:"services"`
	GroupID               string                        `json:"groupId"`
	WorkspaceToolLaunches []WorkspaceToolLaunch         `json:"workspaceToolLaunches"`
	AgentContext          *AgentTaskContext             `json:"agentContext"`
	AgentDocumentLayout   AgentDocumentLayout           `json:"agentDocumentLayout"`
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
type TagOperationState string

const (
	TagOperationCreated            TagOperationState = "CREATED"
	TagOperationPreflightPassed    TagOperationState = "PREFLIGHT_PASSED"
	TagOperationSourceBranchPushed TagOperationState = "SOURCE_BRANCH_PUSHED"
	TagOperationTargetBranchPushed TagOperationState = "TARGET_BRANCH_PUSHED"
	TagOperationLocalTagCreated    TagOperationState = "LOCAL_TAG_CREATED"
	TagOperationTagPushed          TagOperationState = "TAG_PUSHED"
	TagOperationSuccess            TagOperationState = "SUCCESS"
	TagOperationConflict           TagOperationState = "CONFLICT"
	TagOperationFailed             TagOperationState = "FAILED"
	TagOperationPartial            TagOperationState = "PARTIAL"
)

type TagOperation struct {
	OperationID    string              `json:"operationId"`
	FolderName     string              `json:"folderName"`
	ServiceName    string              `json:"serviceName"`
	RepositoryID   string              `json:"repositoryId"`
	SourceBranch   string              `json:"sourceBranch"`
	TargetBranch   *string             `json:"targetBranch"`
	Remote         string              `json:"remote"`
	TagMode        TagBuildMode        `json:"tagMode"`
	State          TagOperationState   `json:"state"`
	CreatedAt      string              `json:"createdAt"`
	UpdatedAt      string              `json:"updatedAt"`
	SourceSHA      *string             `json:"sourceSha"`
	TargetSHA      *string             `json:"targetSha"`
	Tag            *string             `json:"tag"`
	Message        *string             `json:"message"`
	ConflictFiles  []string            `json:"conflictFiles"`
	GroupServiceID string              `json:"groupServiceId"`
	ModuleID       string              `json:"moduleId"`
	GenbuStatus    GenbuTagProbeStatus `json:"genbuStatus"`
	BatchID        *string             `json:"batchId"`
}
type GenbuStageStatus string

const (
	GenbuInitial  GenbuStageStatus = "INITIAL"
	GenbuBuilding GenbuStageStatus = "BUILDING"
	GenbuSuccess  GenbuStageStatus = "SUCCESS"
	GenbuFailed   GenbuStageStatus = "FAILED"
	GenbuUnknown  GenbuStageStatus = "UNKNOWN"
)

type GenbuTagProbeStatus struct {
	Build                         GenbuStageStatus `json:"build"`
	UAT                           GenbuStageStatus `json:"uat"`
	Production                    GenbuStageStatus `json:"production"`
	NotFound                      bool             `json:"notFound"`
	BuiltCompletedAt              *string          `json:"builtCompletedAt"`
	ReleasedCompletedAt           *string          `json:"releasedCompletedAt"`
	ProductionReleasedCompletedAt *string          `json:"productionReleasedCompletedAt"`
	CheckedAt                     *string          `json:"checkedAt"`
	FailureReason                 *string          `json:"failureReason"`
	StoppedByNewerRelease         bool             `json:"stoppedByNewerRelease"`
}
type TagBuildHistoryEntry struct {
	OperationID  string            `json:"operationId"`
	Timestamp    string            `json:"timestamp"`
	FolderName   string            `json:"folderName"`
	ServiceName  string            `json:"serviceName"`
	SourceBranch string            `json:"sourceBranch"`
	TargetBranch *string           `json:"targetBranch"`
	TagMode      TagBuildMode      `json:"tagMode"`
	Tag          *string           `json:"tag"`
	State        TagOperationState `json:"state"`
	Message      *string           `json:"message"`
	BatchID      *string           `json:"batchId"`
}
type TagHistoryItem struct {
	GroupID    string         `json:"groupId"`
	BatchID    *string        `json:"batchId"`
	FolderName string         `json:"folderName"`
	CreatedAt  string         `json:"createdAt"`
	UpdatedAt  string         `json:"updatedAt"`
	Operations []TagOperation `json:"operations"`
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
type DeleteRisk struct {
	ServiceName         string  `json:"serviceName"`
	Staged              bool    `json:"staged"`
	Unstaged            bool    `json:"unstaged"`
	Untracked           bool    `json:"untracked"`
	OperationInProgress *string `json:"operationInProgress"`
	UnpushedCommits     int     `json:"unpushedCommits"`
	StatusCheckError    *string `json:"statusCheckError"`
}
type RemoteBranchRef struct {
	Remote string `json:"remote"`
	Branch string `json:"branch"`
}
type RequirementAiNamingSuggestion struct {
	FolderName   string `json:"folderName"`
	BranchSuffix string `json:"branchSuffix"`
}

const DefaultCommandProxyURL = "http://127.0.0.1:7897"

// An explicitly disabled endpoint must stay disabled when targets were omitted.
func DefaultCommandProxyTargets(endpoint *string) []CommandProxyTarget {
	if value, err := NormalizeCommandProxyURL(endpoint); err == nil && value != nil {
		return []CommandProxyTarget{ProxyTargetCodex}
	}
	return []CommandProxyTarget{}
}

func DefaultAppConfig() AppConfig {
	endpoint := DefaultCommandProxyURL
	return AppConfig{SchemaVersion: CurrentAppConfigSchemaVersion, DefaultBranchPrefix: "", Repositories: []RepositoryConfig{}, Groups: []GroupConfig{{ID: DefaultGroupID, Name: DefaultGroupName, TagEnabled: true, Services: []GroupServiceConfig{}}}, Theme: ThemeSystem, ToolVisibility: ToolVisibilitySettings{JSON: true, DateTime: true, TextDiff: true, KillPort: false}, SidebarVisibility: SidebarVisibilitySettings{Requirements: true, Tools: true, Production: true, Services: true}, TagEnabled: true, TagHistoryMaxGroups: DefaultTagHistoryMaxGroups, DevelopmentTools: []DevelopmentToolConfig{}, DefaultDevelopmentTool: DevelopmentToolIntelliJIdea, DefaultWorkspaceToolIDs: []string{}, BlockedGitWriteBranches: []string{"master", "main"}, MeegleProjects: []MeegleProjectConfig{}, CommandProxyURL: &endpoint, CommandProxyTargets: DefaultCommandProxyTargets(&endpoint), CodexPluginMarketplaceSources: []CodexPluginMarketplaceSource{}, SkillSources: []SkillSource{}, AIRequirementNamingEnabled: true, AIRequirementNamingPrewarmEnabled: true, AIRequirementNamingModel: DefaultAIRequirementNamingModel}
}

const DefaultAIRequirementNamingModel = "gpt-6-luna"

func DefaultTaskManifest() TaskManifest {
	return TaskManifest{SchemaVersion: CurrentTaskManifestSchemaVersion, RequirementLink: "", RequirementMaterials: RequirementMaterialsDirectory{Status: MaterialsNotRequested}, LifecycleStatus: TaskActive, Services: []ServiceWorkspace{}, GroupID: DefaultGroupID, WorkspaceToolLaunches: []WorkspaceToolLaunch{}, AgentDocumentLayout: AgentDocumentReferencedV2}
}

func DefaultWorkspaceHealth(workspaces []ServiceWorkspace) WorkspaceHealth {
	for _, w := range workspaces {
		if w.Health == WorkspaceFailed {
			return WorkspaceFailed
		}
	}
	for _, w := range workspaces {
		if w.Health == WorkspaceCreating {
			return WorkspaceCreating
		}
	}
	for _, w := range workspaces {
		if w.Health == WorkspaceReadyWithWarnings {
			return WorkspaceReadyWithWarnings
		}
	}
	return WorkspaceReady
}

func (s GroupServiceConfig) EffectiveMasterBranch(module ServiceModuleConfig) string {
	if module.MasterBranch != nil {
		return *module.MasterBranch
	}
	return s.MasterBranch
}

func (s GroupServiceConfig) EffectiveMasterRemote(module ServiceModuleConfig) string {
	ref, err := ParseRemoteBranchRef(s.EffectiveMasterBranch(module))
	if err != nil {
		return ""
	}
	return ref.Remote
}

func (s GroupServiceConfig) ResolvedModule(module ServiceModuleConfig) ServiceModuleConfig {
	branch := s.EffectiveMasterBranch(module)
	module.MasterBranch = &branch
	return module
}

func (s GroupServiceConfig) EffectiveTagTargetRef(module ServiceModuleConfig) *string {
	if !module.TagEnabled || module.TagMode != TagBuildMergeToTargetBranch {
		return nil
	}
	if module.TagTargetRef != nil {
		value := *module.TagTargetRef
		return &value
	}
	if s.TestTagBaselineRef == "" {
		return nil
	}
	value := s.TestTagBaselineRef
	return &value
}
