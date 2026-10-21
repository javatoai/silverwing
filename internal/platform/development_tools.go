package platform

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
)

type developmentToolStatus struct {
	Type       domain.DevelopmentToolType `json:"type"`
	Path       string                     `json:"path"`
	Configured bool                       `json:"configured"`
	Exists     bool                       `json:"exists"`
}

type terminalCommandResolution struct {
	DisplayName string `json:"displayName"`
	Command     string `json:"command"`
	Source      string `json:"source"`
	Available   bool   `json:"available"`
}

var developmentToolTypes = []domain.DevelopmentToolType{
	domain.DevelopmentToolIntelliJIdea,
	domain.DevelopmentToolWebStorm,
	domain.DevelopmentToolPyCharm,
	domain.DevelopmentToolVisualStudioCode,
	domain.DevelopmentToolAndroidStudio,
	domain.DevelopmentToolDevEcoStudio,
}

func (s *Service) developmentToolsSnapshot(ctx context.Context, detectMissing bool) (any, error) {
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	added := []domain.DevelopmentToolType{}
	if detectMissing {
		configured := map[domain.DevelopmentToolType]bool{}
		for _, tool := range cfg.DevelopmentTools {
			configured[tool.Type] = true
		}
		missing := []domain.DevelopmentToolType{}
		for _, toolType := range developmentToolTypes {
			if !configured[toolType] {
				missing = append(missing, toolType)
			}
		}
		found := detectDevelopmentTools(ctx, missing)
		if len(found) > 0 {
			var updateErr error
			cfg, updateErr = s.config.Update(func(current domain.AppConfig) (domain.AppConfig, error) {
				present := map[domain.DevelopmentToolType]bool{}
				for _, entry := range current.DevelopmentTools {
					present[entry.Type] = true
				}
				for _, toolType := range developmentToolTypes {
					path, ok := found[toolType]
					if !ok || present[toolType] {
						continue
					}
					current.DevelopmentTools = append(current.DevelopmentTools, domain.DevelopmentToolConfig{Type: toolType, Path: path})
					present[toolType] = true
					added = append(added, toolType)
				}
				return current, nil
			})
			if updateErr != nil {
				return nil, updateErr
			}
		}
	}
	paths := make(map[domain.DevelopmentToolType]string, len(cfg.DevelopmentTools))
	for _, item := range cfg.DevelopmentTools {
		paths[item.Type] = strings.TrimSpace(item.Path)
	}
	tools := make([]developmentToolStatus, 0, len(developmentToolTypes))
	for _, toolType := range developmentToolTypes {
		path := paths[toolType]
		tools = append(tools, developmentToolStatus{
			Type:       toolType,
			Path:       path,
			Configured: path != "",
			Exists:     path != "" && pathExists(path),
		})
	}
	result := map[string]any{
		"tools":              tools,
		"terminalResolution": resolveTerminalCommand(nil),
	}
	if detectMissing {
		result["addedTypes"] = added
	}
	return result, nil
}

func (s *Service) existingDevelopmentToolPaths(payload map[string]any) (any, error) {
	paths, err := decodePayload[map[string]string](payload, "paths")
	if err != nil {
		return nil, err
	}
	existing := []domain.DevelopmentToolType{}
	for _, toolType := range developmentToolTypes {
		path := strings.TrimSpace(paths[string(toolType)])
		if path != "" && pathExists(path) {
			existing = append(existing, toolType)
		}
	}
	return map[string]any{"existingTypes": existing}, nil
}

func (s *Service) resolveTerminal(_ map[string]any) (any, error) {
	return resolveTerminalCommand(nil), nil
}

func resolveTerminalCommand(_ *string) terminalCommandResolution {
	switch runtime.GOOS {
	case "windows":
		return terminalCommandResolution{DisplayName: "系统默认终端", Command: "powershell.exe", Source: "SYSTEM_DEFAULT", Available: true}
	case "darwin":
		return terminalCommandResolution{DisplayName: "Terminal", Command: "Terminal.app", Source: "SYSTEM_DEFAULT", Available: true}
	default:
		return terminalCommandResolution{DisplayName: "系统终端", Command: "x-terminal-emulator", Source: "SYSTEM_DEFAULT", Available: true}
	}
}

func detectDevelopmentTools(ctx context.Context, types []domain.DevelopmentToolType) map[domain.DevelopmentToolType]string {
	result := map[domain.DevelopmentToolType]string{}
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		return result
	}
	for _, toolType := range types {
		for _, candidate := range standardToolCandidates(toolType) {
			if applicationExists(candidate) {
				result[toolType] = candidate
				break
			}
		}
		if result[toolType] == "" {
			for _, candidate := range toolboxToolCandidates(toolType) {
				if applicationExists(candidate) {
					result[toolType] = candidate
					break
				}
			}
		}
		if result[toolType] == "" {
			if candidate := probeDevelopmentToolOnPath(ctx, toolType); candidate != "" && applicationExists(candidate) {
				result[toolType] = candidate
			}
		}
	}
	return result
}

func standardToolCandidates(toolType domain.DevelopmentToolType) []string {
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		roots := []string{"/Applications", filepath.Join(home, "Applications")}
		result := []string{}
		for _, root := range roots {
			for _, name := range macToolApplicationNames(toolType) {
				result = append(result, filepath.Join(root, name))
			}
		}
		return result
	}
	programFiles := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")}
	localPrograms := ""
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		localPrograms = filepath.Join(local, "Programs")
	}
	programFiles = append(programFiles, localPrograms)
	programFiles = nonEmpty(programFiles)
	switch toolType {
	case domain.DevelopmentToolIntelliJIdea, domain.DevelopmentToolWebStorm, domain.DevelopmentToolPyCharm:
		prefix, executable := windowsJetBrainsTool(toolType)
		candidates := []string{}
		for _, base := range programFiles {
			vendor := filepath.Join(base, "JetBrains")
			children := directoryChildren(vendor)
			sortDirectoriesByModTime(children)
			for _, child := range children {
				if strings.HasPrefix(strings.ToLower(filepath.Base(child)), strings.ToLower(prefix)) {
					candidates = append(candidates, filepath.Join(child, "bin", executable))
				}
			}
		}
		return candidates
	case domain.DevelopmentToolVisualStudioCode:
		return nonEmpty([]string{
			rootJoin(localPrograms, "Microsoft VS Code", "Code.exe"),
			rootJoin(os.Getenv("ProgramFiles"), "Microsoft VS Code", "Code.exe"),
			rootJoin(os.Getenv("ProgramFiles(x86)"), "Microsoft VS Code", "Code.exe"),
		})
	case domain.DevelopmentToolAndroidStudio:
		return nonEmpty([]string{
			rootJoin(os.Getenv("ProgramFiles"), "Android", "Android Studio", "bin", "studio64.exe"),
			rootJoin(os.Getenv("ProgramFiles(x86)"), "Android", "Android Studio", "bin", "studio64.exe"),
			rootJoin(localPrograms, "Android Studio", "bin", "studio64.exe"),
			rootJoin(localPrograms, "Google", "Android Studio", "bin", "studio64.exe"),
		})
	case domain.DevelopmentToolDevEcoStudio:
		return nonEmpty([]string{
			rootJoin(os.Getenv("ProgramFiles"), "Huawei", "DevEco Studio", "bin", "devecostudio64.exe"),
			rootJoin(os.Getenv("ProgramFiles(x86)"), "Huawei", "DevEco Studio", "bin", "devecostudio64.exe"),
			rootJoin(localPrograms, "Huawei", "DevEco Studio", "bin", "devecostudio64.exe"),
			rootJoin(localPrograms, "DevEco Studio", "bin", "devecostudio64.exe"),
		})
	default:
		return nil
	}
}

func toolboxToolCandidates(toolType domain.DevelopmentToolType) []string {
	aliases := toolboxProductAliases(toolType)
	if len(aliases) == 0 {
		return nil
	}
	root := ""
	if runtime.GOOS == "windows" {
		local := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if local == "" {
			return nil
		}
		root = filepath.Join(local, "JetBrains", "Toolbox", "apps")
	} else {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, "Library", "Application Support", "JetBrains", "Toolbox", "apps")
	}
	candidates := []string{}
	for _, alias := range aliases {
		stable := filepath.Join(root, alias, "ch-0")
		for _, build := range directoryChildren(stable) {
			if runtime.GOOS == "windows" {
				_, executable := windowsJetBrainsTool(toolType)
				if toolType == domain.DevelopmentToolAndroidStudio {
					executable = "studio64.exe"
				} else if toolType == domain.DevelopmentToolDevEcoStudio {
					executable = "devecostudio64.exe"
				}
				candidates = append(candidates, filepath.Join(build, "bin", executable))
			} else {
				for _, name := range macToolApplicationNames(toolType) {
					candidates = append(candidates, filepath.Join(build, name))
				}
			}
		}
	}
	if runtime.GOOS == "windows" {
		sortCandidatePathsByInstallDirectory(candidates, 2)
	} else {
		sortCandidatePathsByInstallDirectory(candidates, 1)
	}
	return candidates
}

var developmentDiscoveryRunner = process.NewRunner()

func probeDevelopmentToolOnPath(ctx context.Context, toolType domain.DevelopmentToolType) string {
	resolver := executable.NewResolver(developmentDiscoveryRunner)
	for _, name := range pathToolCommandNames(toolType) {
		path, _, err := resolver.PrepareCommand(ctx, name)
		if err != nil {
			return ""
		}
		if filepath.IsAbs(path) && applicationExists(path) {
			return filepath.Clean(path)
		}
	}
	return ""
}

func applicationExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if runtime.GOOS == "darwin" && strings.HasSuffix(strings.ToLower(path), ".app") {
		return info.IsDir()
	}
	return info.Mode().IsRegular()
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func directoryChildren(directory string) []string {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	children := make([]string, 0, len(entries))
	for _, entry := range entries {
		child := filepath.Join(directory, entry.Name())
		if info, statErr := os.Stat(child); statErr == nil && info.IsDir() {
			children = append(children, child)
		}
	}
	return children
}

func sortDirectoriesByModTime(paths []string) {
	sort.SliceStable(paths, func(i, j int) bool {
		left, leftErr := os.Stat(paths[i])
		right, rightErr := os.Stat(paths[j])
		if leftErr == nil && rightErr == nil && !left.ModTime().Equal(right.ModTime()) {
			return left.ModTime().After(right.ModTime())
		}
		return paths[i] > paths[j]
	})
}

func sortCandidatePathsByInstallDirectory(paths []string, levels int) {
	sort.SliceStable(paths, func(i, j int) bool {
		installDir := func(candidate string) string {
			for range levels {
				candidate = filepath.Dir(candidate)
			}
			return candidate
		}
		left, leftErr := os.Stat(installDir(paths[i]))
		right, rightErr := os.Stat(installDir(paths[j]))
		if leftErr == nil && rightErr == nil && !left.ModTime().Equal(right.ModTime()) {
			return left.ModTime().After(right.ModTime())
		}
		return paths[i] > paths[j]
	})
}

func windowsJetBrainsTool(toolType domain.DevelopmentToolType) (prefix, executable string) {
	switch toolType {
	case domain.DevelopmentToolIntelliJIdea:
		return "IntelliJ IDEA", "idea64.exe"
	case domain.DevelopmentToolWebStorm:
		return "WebStorm", "webstorm64.exe"
	case domain.DevelopmentToolPyCharm:
		return "PyCharm", "pycharm64.exe"
	case domain.DevelopmentToolAndroidStudio:
		return "", "studio64.exe"
	case domain.DevelopmentToolDevEcoStudio:
		return "", "devecostudio64.exe"
	default:
		return "", ""
	}
}

func toolboxProductAliases(toolType domain.DevelopmentToolType) []string {
	switch toolType {
	case domain.DevelopmentToolIntelliJIdea:
		return []string{"IDEA-U", "IDEA-C"}
	case domain.DevelopmentToolWebStorm:
		return []string{"WebStorm"}
	case domain.DevelopmentToolPyCharm:
		return []string{"PyCharm-P", "PyCharm-C"}
	default:
		return nil
	}
}

func pathToolCommandNames(toolType domain.DevelopmentToolType) []string {
	switch toolType {
	case domain.DevelopmentToolIntelliJIdea:
		return []string{"idea64.exe", "idea.exe", "idea"}
	case domain.DevelopmentToolWebStorm:
		return []string{"webstorm64.exe", "webstorm.exe", "webstorm"}
	case domain.DevelopmentToolPyCharm:
		return []string{"pycharm64.exe", "pycharm.exe", "pycharm"}
	case domain.DevelopmentToolVisualStudioCode:
		return []string{"Code.exe", "code.cmd", "code"}
	case domain.DevelopmentToolAndroidStudio:
		return []string{"studio64.exe", "studio.exe", "studio"}
	case domain.DevelopmentToolDevEcoStudio:
		return []string{"devecostudio64.exe", "devecostudio.exe", "devecostudio"}
	default:
		return nil
	}
}

func macToolApplicationNames(toolType domain.DevelopmentToolType) []string {
	switch toolType {
	case domain.DevelopmentToolIntelliJIdea:
		return []string{"IntelliJ IDEA.app", "IntelliJ IDEA CE.app"}
	case domain.DevelopmentToolWebStorm:
		return []string{"WebStorm.app"}
	case domain.DevelopmentToolPyCharm:
		return []string{"PyCharm.app", "PyCharm CE.app"}
	case domain.DevelopmentToolVisualStudioCode:
		return []string{"Visual Studio Code.app"}
	case domain.DevelopmentToolAndroidStudio:
		return []string{"Android Studio.app"}
	case domain.DevelopmentToolDevEcoStudio:
		return []string{"DevEco-Studio.app", "DevEco Studio.app"}
	default:
		return nil
	}
}

func nonEmpty(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}

func rootJoin(root string, parts ...string) string {
	if strings.TrimSpace(root) == "" {
		return ""
	}
	return filepath.Join(append([]string{root}, parts...)...)
}

func stringPointerOrNil(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	copy := strings.TrimSpace(value)
	return &copy
}
