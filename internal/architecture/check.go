// Package architecture checks source dependency directions without loading the
// desktop framework or executing application code.
package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Violation identifies the source dependency and the responsibility it crosses.
// File is relative to the module root and always uses forward slashes.
type Violation struct {
	File   string
	Line   int
	Import string
	Rule   string
	Reason string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s:%d: %s: %s (%s)", v.File, v.Line, v.Rule, v.Reason, v.Import)
}

// Check parses every non-test Go source under internal, including sources for
// other operating systems and inactive build tags. This intentionally does not
// use go list or the current build context: a Windows-only reverse dependency
// must also fail on macOS. Nested modules, vendor and testdata are separate
// source universes and are not scanned.
func Check(root string) ([]Violation, error) {
	module, err := modulePath(root)
	if err != nil {
		return nil, err
	}
	internal := filepath.Join(root, "internal")
	info, err := os.Stat(internal)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", internal)
	}
	checker := sourceChecker{module: module, standard: make(map[string]bool)}
	var violations []Violation
	err = filepath.WalkDir(internal, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" || entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			if path != internal {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				} else if !os.IsNotExist(err) {
					return err
				}
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", relative, err)
		}
		packagePath := module + "/" + filepath.ToSlash(filepath.Dir(relative))
		sharedEditor := ownsSharedEditor(file)
		for _, dependency := range file.Imports {
			importPath, err := strconv.Unquote(dependency.Path.Value)
			if err != nil {
				return fmt.Errorf("parse import in %s: %w", relative, err)
			}
			for _, problem := range checker.importProblems(packagePath, importPath, sharedEditor) {
				violations = append(violations, Violation{
					File: relative, Line: fileSet.Position(dependency.Pos()).Line,
					Import: importPath, Rule: problem.rule, Reason: problem.reason,
				})
			}
		}
		if inPackage(packagePath, module+"/internal/documents") {
			violations = append(violations, remoteCLIProblems(fileSet, file, relative)...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].File != violations[j].File {
			return violations[i].File < violations[j].File
		}
		if violations[i].Line != violations[j].Line {
			return violations[i].Line < violations[j].Line
		}
		return violations[i].Rule < violations[j].Rule
	})
	return violations, nil
}

type sourceChecker struct {
	module   string
	standard map[string]bool
}

type problem struct{ rule, reason string }

func (c *sourceChecker) importProblems(source, dependency string, sharedEditor bool) []problem {
	internal := c.module + "/internal/"
	var problems []problem
	add := func(rule, reason string) { problems = append(problems, problem{rule, reason}) }
	if inPackage(source, internal+"domain") {
		if (strings.HasPrefix(dependency, internal) && !inPackage(dependency, internal+"domain") && !c.purePackage(dependency)) || inPackage(dependency, "github.com/egoist/mygo") {
			add("domain-direction", "domain may depend on pure text/value packages, not application, editor, desktop or infrastructure")
		}
	}
	if c.purePackage(source) {
		goldmark := inPackage(source, internal+"markdowntext") && inPackage(dependency, "github.com/yuin/goldmark")
		if !c.standardImport(dependency) && !c.purePackage(dependency) && !goldmark {
			add("pure-package", "pure packages may use the standard library and other pure packages; only markdowntext may use Goldmark")
		}
	}
	if inPackage(source, internal+"application") && (inPackage(dependency, internal+"desktop") || inPackage(dependency, "github.com/egoist/mygo")) {
		add("application-ui", "application must remain independent of desktop and MyGO")
	}
	if inPackage(source, internal+"documents") {
		for _, adapter := range []string{"integrations", "requirementprovider", "process", "commandruntime"} {
			if inPackage(dependency, internal+adapter) {
				add("documents-provider", "documents receives RequirementProvider; source adapters and remote CLI protocols belong outside documents")
				break
			}
		}
	}
	if sharedEditor && inPackage(dependency, internal+"workspace") {
		add("shared-editor-language", "shared TextEditor and its language mapping use filelanguage rather than workspace business types")
	}
	return problems
}

func (c *sourceChecker) purePackage(packagePath string) bool {
	for _, name := range []string{"markdowntext", "filelanguage", "asyncjob", "productprofile"} {
		if inPackage(packagePath, c.module+"/internal/"+name) {
			return true
		}
	}
	return false
}

func (c *sourceChecker) standardImport(importPath string) bool {
	if known, ok := c.standard[importPath]; ok {
		return known
	}
	// Consult the toolchain's source tree instead of treating every import with
	// no dot in its first component as standard (this module also has no dot).
	standard := false
	if importPath != "C" && !strings.HasPrefix(importPath, ".") && !strings.Contains(strings.Split(importPath, "/")[0], ".") {
		info, err := os.Stat(filepath.Join(runtime.GOROOT(), "src", filepath.FromSlash(importPath)))
		standard = err == nil && info.IsDir()
	}
	c.standard[importPath] = standard
	return standard
}

func inPackage(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// These declarations identify the shared component's responsibility rather
// than imposing filenames on DEV, QA or Product's differently named sources.
func ownsSharedEditor(file *ast.File) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil {
			continue
		}
		switch function.Name.Name {
		case "TextEditor", "textEditorLanguage", "workspaceFilePreviewLanguage":
			return true
		}
	}
	return false
}

// Native clipboard and trash commands remain legal in documents. Detect direct
// remote CLI entry points, including aliases and literal resolver wrappers,
// without banning os/exec or executable used by those native file operations.
// This is an entry-point check, not whole-program command/data-flow analysis.
func remoteCLIProblems(fileSet *token.FileSet, file *ast.File, relative string) []Violation {
	aliases := make(map[string]bool)
	for _, dependency := range file.Imports {
		importPath, _ := strconv.Unquote(dependency.Path.Value)
		if importPath != "os/exec" {
			continue
		}
		alias := "exec"
		if dependency.Name != nil {
			alias = dependency.Name.Name
		}
		aliases[alias] = true
	}
	var violations []Violation
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		function := ""
		switch target := call.Fun.(type) {
		case *ast.SelectorExpr:
			if qualifier, ok := target.X.(*ast.Ident); ok && aliases[qualifier.Name] {
				function = target.Sel.Name
			}
		case *ast.Ident:
			if aliases["."] {
				function = target.Name
			}
		}
		argument := 0
		switch function {
		case "CommandContext":
			argument = 1
		case "Command", "LookPath":
		default:
			return true
		}
		if len(call.Args) > argument {
			if command := remoteCLIName(call.Args[argument]); command != "" {
				violations = append(violations, Violation{File: relative, Line: fileSet.Position(call.Pos()).Line,
					Import: "os/exec", Rule: "documents-remote-cli", Reason: "remote command " + command + " belongs in a requirement/source adapter"})
			}
		}
		return true
	})
	return violations
}

func remoteCLIName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return ""
		}
		literal, err := strconv.Unquote(value.Value)
		if err != nil {
			return ""
		}
		name := strings.ToLower(filepath.Base(strings.ReplaceAll(literal, `\`, "/")))
		for _, suffix := range []string{".exe", ".cmd", ".bat"} {
			name = strings.TrimSuffix(name, suffix)
		}
		switch name {
		case "meegle", "lark", "genbu":
			return name
		}
	case *ast.CallExpr:
		for _, argument := range value.Args {
			if name := remoteCLIName(argument); name != "" {
				return name
			}
		}
	}
	return ""
}

func modulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.SplitN(line, "//", 2)[0])
		if len(fields) == 2 && fields[0] == "module" {
			module := fields[1]
			if strings.HasPrefix(module, `"`) {
				module, err = strconv.Unquote(module)
			}
			if err == nil && module != "" {
				return module, nil
			}
		}
	}
	return "", fmt.Errorf("no module directive in %s", filepath.Join(root, "go.mod"))
}
