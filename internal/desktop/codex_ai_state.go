package desktop

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

const (
	codexAITabMCP      = "MCP"
	codexAITabSkills   = "Skills"
	codexAITabGuides   = "说明文件"
	codexAITabSessions = "会话"
)

type codexAIIdentity struct {
	home, taskID, taskCreatedAt, taskPath, cwd string
}

func (identity codexAIIdentity) key() string {
	return strings.Join([]string{
		identity.home,
		identity.taskID,
		identity.taskCreatedAt,
		normalizeIdentityPath(identity.taskPath),
		normalizeIdentityPath(identity.cwd),
	}, "\x00")
}

type codexAIState struct {
	Open bool

	identity         codexAIIdentity
	identityRevision uint64
	tab              string
	mcpScope         string

	snapshot         *codexAISnapshot
	snapshotLoading  bool
	snapshotLoaded   bool
	snapshotError    string
	snapshotRevision uint64

	threads        []codexAIThread
	threadsLoading bool
	threadsLoaded  bool
	threadsError   string
	boundThreadID  string
	selectedThread string
	bindLoading    bool
	bindError      string
	bindMessage    string

	filePath        string
	fileContent     string
	fileCodePreview nativeCodePreviewState
	fileMarkdown    *markdownEditorState
	fileLoading     bool
	fileError       string
	fileRevision    uint64
	externalPath    string
	externalError   string
	externalMessage string

	mcpManaging        bool
	mcpSnapshot        *codexAISnapshot
	mcpStatusLoading   bool
	mcpCheckLoading    bool
	mcpMutationLoading bool
	mcpAuthLoading     bool
	mcpError           string
	mcpCheckError      string
	mcpMessage         string
	mcpConnections     map[string]codexAIMCPConnection
	mcpSwitches        map[string]*bool
	mcpEditor          *codexAIMCPForm
	mcpDelete          *codexAIMCPDelete
	newSessionLoading  bool
	newSessionError    string
	newSessionMessage  string

	requests    map[string]*codexAIRequest
	generations map[string]uint64
	mu          sync.Mutex
	running     map[*codexAIRequest]struct{}
	closed      bool
	cleanupErr  error
}

type codexAISnapshot struct {
	CWD      *string                  `json:"cwd"`
	UserFile string                   `json:"userFile"`
	Layers   []codexAIConfigLayer     `json:"layers"`
	Servers  []codexAIMCPServer       `json:"servers"`
	Skills   []codexAISkill           `json:"skills"`
	Files    []codexAIInstructionFile `json:"files"`
	Warnings []string                 `json:"warnings"`
}

type codexAIConfigLayer struct {
	File           string         `json:"file"`
	Type           string         `json:"type"`
	Version        string         `json:"version"`
	Config         map[string]any `json:"config"`
	DisabledReason string         `json:"disabledReason"`
	Fingerprint    string         `json:"fingerprint"`
}

type codexAIMCPServer struct {
	Name     string         `json:"name"`
	Scope    string         `json:"scope"`
	File     string         `json:"file"`
	Version  string         `json:"version"`
	Raw      map[string]any `json:"raw"`
	Active   bool           `json:"active"`
	PluginID string         `json:"pluginId"`
	Note     string         `json:"note"`
}

type codexAISkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Scope       string `json:"scope"`
	Enabled     bool   `json:"enabled"`
}

type codexAIInstructionFile struct {
	Title  string `json:"title"`
	File   string `json:"file"`
	Source string `json:"source"`
	Exists bool   `json:"exists"`
}

type codexAIThread struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	CWD   string `json:"cwd"`
}

type codexAIThreadsResult struct {
	Threads       []codexAIThread `json:"threads"`
	BoundThreadID *string         `json:"boundThreadId"`
}

func codexAITab(tab string) string {
	switch strings.TrimSpace(tab) {
	case codexAITabSkills:
		return codexAITabSkills
	case codexAITabGuides:
		return codexAITabGuides
	case codexAITabSessions:
		return codexAITabSessions
	default:
		return codexAITabMCP
	}
}

func (s *app) openCodexAI(taskPath, tab string) {
	current := s.currentTask()
	if current.id == "" || strings.TrimSpace(current.path) == "" {
		s.lastError = "请先选择一个有任务目录的任务。"
		return
	}
	if strings.TrimSpace(taskPath) == "" {
		taskPath = current.path
	}
	requested, err := filepath.Abs(taskPath)
	if err != nil {
		s.lastError = "Codex 任务目录无效：" + err.Error()
		return
	}
	currentPath, err := filepath.Abs(current.path)
	if err != nil || normalizeIdentityPath(requested) != normalizeIdentityPath(currentPath) {
		s.lastError = "Codex 任务目录与当前选中任务不一致。"
		return
	}
	identity := codexAIIdentity{
		home: s.snapshot.Home, taskID: current.id, taskCreatedAt: current.createdAt,
		taskPath: filepath.Clean(currentPath), cwd: filepath.Clean(requested),
	}
	state := s.codexAI
	if state == nil {
		state = &codexAIState{
			requests:    make(map[string]*codexAIRequest),
			generations: make(map[string]uint64), running: make(map[*codexAIRequest]struct{}),
			mcpScope: "ALL",
		}
		s.codexAI = state
		state.identity = identity
		state.identityRevision = 1
	} else if state.closed {
		s.lastError = "Codex 配置面板已随窗口关闭。"
		return
	} else if state.identity.key() != identity.key() {
		s.invalidateCodexAIRequests(state)
		state.identity = identity
		state.identityRevision++
		state.tab = ""
		state.mcpScope = "ALL"
		state.snapshot = nil
		state.snapshotLoaded = false
		state.snapshotLoading = false
		state.snapshotError = ""
		state.snapshotRevision = 0
		state.threads = nil
		state.threadsLoaded = false
		state.threadsLoading = false
		state.threadsError = ""
		state.boundThreadID = ""
		state.selectedThread = ""
		state.bindLoading = false
		state.bindError = ""
		state.bindMessage = ""
		state.filePath = ""
		state.fileContent = ""
		state.resetFilePreview()
		state.fileLoading = false
		state.fileError = ""
		state.fileRevision = 0
		state.externalPath = ""
		state.externalError = ""
		resetCodexAIMCPState(state)
		state.newSessionLoading = false
		state.newSessionError = ""
		state.newSessionMessage = ""
	}
	state.Open = true
	state.tab = codexAITab(tab)
	s.requestCodexAISnapshot()
	if state.tab == codexAITabSessions {
		s.requestCodexAIThreads()
	}
}

func (s *app) closeCodexAI() {
	state := s.codexAI
	if state == nil {
		return
	}
	state.Open = false
	state.filePath = ""
	state.fileContent = ""
	state.resetFilePreview()
	resetCodexAIMCPState(state)
	state.newSessionLoading = false
	state.newSessionError = ""
	state.newSessionMessage = ""
	s.invalidateCodexAIRequests(state)
}

func resetCodexAIMCPState(state *codexAIState) {
	if state == nil {
		return
	}
	state.mcpManaging = false
	state.mcpSnapshot = nil
	state.mcpStatusLoading = false
	state.mcpCheckLoading = false
	state.mcpMutationLoading = false
	state.mcpAuthLoading = false
	state.mcpError = ""
	state.mcpCheckError = ""
	state.mcpMessage = ""
	state.mcpConnections = nil
	state.mcpSwitches = nil
	state.mcpEditor = nil
	state.mcpDelete = nil
}

func (s *app) codexAIIdentityCurrent(state *codexAIState) bool {
	if state == nil || s.codexAI != state || !state.Open || state.closed {
		return false
	}
	current := s.currentTask()
	if current.id != state.identity.taskID || current.createdAt != state.identity.taskCreatedAt || s.snapshot.Home != state.identity.home {
		return false
	}
	currentPath, err := filepath.Abs(current.path)
	if err != nil {
		return false
	}
	return normalizeIdentityPath(currentPath) == normalizeIdentityPath(state.identity.taskPath) &&
		normalizeIdentityPath(state.identity.cwd) == normalizeIdentityPath(state.identity.taskPath)
}

func (s *app) invalidateCodexAIRequests(state *codexAIState) {
	if state == nil {
		return
	}
	for key, request := range state.requests {
		request.cancel()
		delete(state.requests, key)
		state.generations[key]++
	}
	state.snapshotLoading = false
	state.threadsLoading = false
	state.bindLoading = false
	state.fileLoading = false
}

func codexAIPathKey(path string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	return normalizeIdentityPath(filepath.Clean(path))
}

func codexAIErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return fmt.Sprintf("Codex 操作失败（%T）", err)
	}
	return message
}
