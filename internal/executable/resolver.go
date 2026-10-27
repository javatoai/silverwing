// Package executable centralizes local CLI command discovery and provenance.
package executable

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"silverwing-go/internal/process"
)

type Tool string

const (
	ToolCodex  Tool = "codex"
	ToolGit    Tool = "git"
	ToolMeegle Tool = "meegle"
)

func supportedTool(tool Tool) bool {
	switch tool {
	case ToolCodex, ToolGit, ToolMeegle:
		return true
	default:
		return false
	}
}

type Source string

const (
	SourceConfigured   Source = "CONFIGURED"
	SourceDesktopApp   Source = "DESKTOP_APP"
	SourceProbed       Source = "PROBED"
	SourcePathFallback Source = "PATH_FALLBACK"
)

type Resolution struct {
	Command string
	Source  Source
}

type Runner interface {
	Run(context.Context, process.Spec) (process.Result, error)
}

type Resolver struct {
	runner           Runner
	mu               sync.Mutex
	cache            map[string]Resolution
	cacheEpoch       map[string]uint64
	loginPath        string
	loginPathLoaded  bool
	loginPathLoading chan struct{}
	loginPathEpoch   uint64
}

type resolverIdentity struct {
	typeName string
	address  uintptr
}

var resolverRegistry = struct {
	sync.Mutex
	items map[resolverIdentity]*Resolver
}{items: map[resolverIdentity]*Resolver{}}

var defaultRunner = process.NewRunner()

func NewResolver(runner Runner) *Resolver {
	if runner == nil {
		runner = defaultRunner
	}
	value := reflect.ValueOf(runner)
	if value.IsValid() && value.Kind() == reflect.Pointer && !value.IsNil() {
		key := resolverIdentity{typeName: reflect.TypeOf(runner).String(), address: value.Pointer()}
		resolverRegistry.Lock()
		defer resolverRegistry.Unlock()
		if existing := resolverRegistry.items[key]; existing != nil {
			return existing
		}
		created := &Resolver{runner: runner, cache: map[string]Resolution{}}
		resolverRegistry.items[key] = created
		return created
	}
	return &Resolver{runner: runner, cache: map[string]Resolution{}}
}

// Resolve uses an explicit setting first, then the platform discovery rules,
// and finally the historical bare command so callers retain the usual PATH error.
func (r *Resolver) Resolve(ctx context.Context, tool Tool, configured string) Resolution {
	if !supportedTool(tool) {
		return Resolution{}
	}
	key := resolutionCacheKey(tool, configured)
	r.mu.Lock()
	epoch := r.cacheEpoch[key]
	if cached, ok := r.cache[key]; ok {
		r.mu.Unlock()
		return cached
	}
	r.mu.Unlock()
	resolution := r.resolve(ctx, tool, configured)
	if ctx.Err() == nil {
		r.mu.Lock()
		if epoch == r.cacheEpoch[key] {
			r.cache[key] = resolution
		}
		r.mu.Unlock()
	}
	return resolution
}

// ResolveFresh drops this tool/configuration's cached discovery and probes again.
func (r *Resolver) ResolveFresh(ctx context.Context, tool Tool, configured string) Resolution {
	if !supportedTool(tool) {
		return Resolution{}
	}
	key := resolutionCacheKey(tool, configured)
	r.mu.Lock()
	delete(r.cache, key)
	if r.cacheEpoch == nil {
		r.cacheEpoch = map[string]uint64{}
	}
	r.cacheEpoch[key]++
	r.loginPathEpoch++
	r.loginPath, r.loginPathLoaded = "", false
	r.mu.Unlock()
	return r.Resolve(ctx, tool, configured)
}

func resolutionCacheKey(tool Tool, configured string) string {
	return string(tool) + "\x00" + strings.TrimSpace(configured)
}

func (r *Resolver) resolve(ctx context.Context, tool Tool, configured string) Resolution {
	if !supportedTool(tool) {
		return Resolution{}
	}
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return Resolution{Command: configured, Source: SourceConfigured}
	}

	if tool == ToolCodex {
		if desktop := r.desktopCodex(); desktop != "" {
			return Resolution{Command: desktop, Source: SourceDesktopApp}
		}
	}

	if command := r.probe(ctx, tool); command != "" {
		return Resolution{Command: command, Source: SourceProbed}
	}
	return Resolution{Command: fallback(tool, runtime.GOOS), Source: SourcePathFallback}
}

// Environment prepares only the child's environment. Login-shell state is
// never installed in the desktop process, and operation-specific overrides
// (such as an isolated Git index) remain the caller's responsibility.
func (r *Resolver) Environment(ctx context.Context, tool Tool, command string) map[string]string {
	if tool != "" && !supportedTool(tool) {
		return nil
	}
	result := map[string]string{}
	if runtime.GOOS == "darwin" {
		result["PATH"] = mergedPath(command, r.macLoginShellPath(ctx), os.Getenv("PATH"))
	}
	return result
}

func mergedPath(command, loginPath, inherited string) string {
	var entries []string
	if filepath.IsAbs(command) {
		entries = append(entries, filepath.Dir(command))
	}
	entries = append(entries, filepath.SplitList(loginPath)...)
	entries = append(entries, filepath.SplitList(inherited)...)
	seen := map[string]bool{}
	merged := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" || seen[entry] {
			continue
		}
		seen[entry] = true
		merged = append(merged, entry)
	}
	return strings.Join(merged, string(os.PathListSeparator))
}

// Prepare also resolves bare commands against the effective child PATH.
// exec.Command resolves before Cmd.Env is assigned, so setting PATH alone
// cannot fix a GUI application's command-not-found error.
func (r *Resolver) Prepare(ctx context.Context, tool Tool, configured string) (Resolution, map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return Resolution{}, nil, err
	}
	if !supportedTool(tool) {
		return Resolution{}, nil, fmt.Errorf("不支持的 CLI：%s", tool)
	}
	resolution := r.Resolve(ctx, tool, configured)
	command, env, err := r.prepareCommand(ctx, tool, resolution.Command)
	resolution.Command = command
	return resolution, env, err
}

func (r *Resolver) PrepareCommand(ctx context.Context, command string) (string, map[string]string, error) {
	return r.prepareCommand(ctx, "", command)
}

func (r *Resolver) prepareCommand(ctx context.Context, tool Tool, command string) (string, map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	env := r.Environment(ctx, tool, command)
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if !strings.ContainsAny(command, `/\\`) {
		if searchPath, ok := env["PATH"]; ok {
			if resolved := executableOnPath(command, searchPath); resolved != "" {
				command = resolved
			}
		} else if resolved, err := exec.LookPath(command); err == nil {
			command = resolved
		}
	}
	return command, env, nil
}

func executableOnPath(command, searchPath string) string {
	for _, directory := range filepath.SplitList(searchPath) {
		// An empty/relative PATH entry must not implicitly execute a file from
		// an arbitrary repository or the desktop process's working directory.
		if !filepath.IsAbs(directory) {
			continue
		}
		candidate := filepath.Join(directory, command)
		if isExecutableFile(candidate, runtime.GOOS) {
			return candidate
		}
	}
	return ""
}

// SystemCommand avoids user PATH discovery for macOS's fixed system tools.
func SystemCommand(name string) string {
	if runtime.GOOS != "darwin" {
		return name
	}
	switch name {
	case "open", "osascript", "pbpaste", "pbcopy", "sips":
		return "/usr/bin/" + name
	case "ps":
		return "/bin/ps"
	case "lsof":
		return "/usr/sbin/lsof"
	}
	return name
}

func (r *Resolver) macLoginShellPath(ctx context.Context) string {
	for {
		r.mu.Lock()
		if r.loginPathLoaded {
			path := r.loginPath
			r.mu.Unlock()
			return path
		}
		if loading := r.loginPathLoading; loading != nil {
			r.mu.Unlock()
			select {
			case <-loading:
				continue
			case <-ctx.Done():
				return ""
			}
		}
		r.loginPathLoading = make(chan struct{})
		epoch := r.loginPathEpoch
		r.mu.Unlock()
		result, err := r.runner.Run(ctx, process.Spec{
			Executable: "/bin/zsh",
			Arguments:  []string{"-lc", `printf '%s\n' "$PATH"`},
			Timeout:    3 * time.Second,
		})
		path := ""
		if err == nil && result.ExitCode == 0 {
			lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
			if len(lines) > 0 {
				path = strings.TrimSpace(lines[len(lines)-1])
			}
		}
		r.mu.Lock()
		if epoch == r.loginPathEpoch && ctx.Err() == nil && err == nil && result.ExitCode == 0 && path != "" {
			r.loginPath, r.loginPathLoaded = path, true
		}
		close(r.loginPathLoading)
		r.loginPathLoading = nil
		r.mu.Unlock()
		if ctx.Err() != nil {
			return ""
		}
		return path
	}
}

func (r *Resolver) probe(ctx context.Context, tool Tool) string {
	if !supportedTool(tool) {
		return ""
	}
	if runtime.GOOS == "darwin" {
		name := string(tool)
		return executableOnPath(name, mergedPath("", r.macLoginShellPath(ctx), os.Getenv("PATH")))
	}
	commands := probeCommands(tool, runtime.GOOS)
	for _, command := range commands {
		if len(command) == 0 {
			continue
		}
		result, err := r.runner.Run(ctx, process.Spec{
			Executable: command[0],
			Arguments:  command[1:],
			Timeout:    5 * time.Second,
		})
		if err != nil || result.ExitCode != 0 {
			continue
		}
		for _, line := range strings.Split(result.Stdout, "\n") {
			line = strings.Trim(strings.TrimSpace(line), `"`)
			if !isAbsoluteProbeResult(line, runtime.GOOS) {
				continue
			}

			return line
		}
	}
	return ""
}

func probeCommands(tool Tool, goos string) [][]string {
	if goos == "windows" {
		switch tool {
		case ToolCodex:
			return [][]string{{"where.exe", "codex.exe"}, {"where.exe", "codex.cmd"}}
		case ToolGit:
			return [][]string{{"where.exe", "git.exe"}}
		case ToolMeegle:
			return [][]string{{"where.exe", "meegle.cmd"}}
		}
		return nil
	}
	name := string(tool)
	if goos == "darwin" {
		return [][]string{{"/bin/zsh", "-lc", "command -v " + name}}
	}
	return [][]string{{"/bin/bash", "-lc", "command -v " + name}}
}

var windowsAbsolutePath = regexp.MustCompile(`^[A-Za-z]:[\\/].+`)

func isAbsoluteProbeResult(path, goos string) bool {
	if goos == "windows" {
		return windowsAbsolutePath.MatchString(path)
	}
	return strings.HasPrefix(path, "/")
}

func (r *Resolver) desktopCodex() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	localAppData := strings.TrimSpace(environmentValue("LOCALAPPDATA"))
	if strings.TrimSpace(localAppData) == "" {
		profile := strings.TrimSpace(environmentValue("USERPROFILE"))
		if strings.TrimSpace(profile) == "" {
			profile, _ = os.UserHomeDir()
			profile = strings.TrimSpace(profile)
		}
		if strings.TrimSpace(profile) == "" {
			return ""
		}
		localAppData = filepath.Join(profile, "AppData", "Local")
	}
	root := filepath.Join(localAppData, "OpenAI", "Codex", "bin")
	candidates := []string{filepath.Join(root, "codex.exe")}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			candidates = append(candidates, filepath.Join(root, entry.Name(), "codex.exe"))
		}
	}
	valid := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if isExecutableFile(candidate, runtime.GOOS) {
			valid = append(valid, candidate)
		}
	}
	sort.Slice(valid, func(i, j int) bool {
		left, leftErr := os.Stat(valid[i])
		right, rightErr := os.Stat(valid[j])
		if leftErr == nil && rightErr == nil && !left.ModTime().Equal(right.ModTime()) {
			return left.ModTime().After(right.ModTime())
		}
		return valid[i] > valid[j]
	})
	if len(valid) == 0 {
		return ""
	}
	return valid[0]
}

func environmentValue(name string) string {
	for _, entry := range os.Environ() {
		key, value, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func isExecutableFile(path, goos string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return goos == "windows" || info.Mode().Perm()&0o111 != 0
}

func fallback(tool Tool, goos string) string {
	switch tool {
	case ToolCodex:
		return "codex"
	case ToolGit:
		return "git"
	case ToolMeegle:
		if goos == "windows" {
			return "meegle.cmd"
		}
		return "meegle"
	default:
		return ""
	}
}
