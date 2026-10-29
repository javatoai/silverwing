package application

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

func TestActionPolicyCoversTypedAdapterOperations(t *testing.T) {
	source, err := parser.ParseFile(token.NewFileSet(), "task_action_adapter.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(source, func(node ast.Node) bool {
		branch, ok := node.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expression := range branch.List {
			literal, ok := expression.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			kind, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			descriptor := DescribeAction(kind)
			if !descriptor.Known || descriptor.Kind != kind || descriptor.Label == "" {
				t.Errorf("typed operation has no complete policy: %s", kind)
			}
		}
		return true
	})
	for kind, descriptor := range actionDescriptors {
		if descriptor.Kind != kind || !descriptor.Known || descriptor.Label == "" {
			t.Errorf("inconsistent policy: %s %+v", kind, descriptor)
		}
	}
}
func TestActionPolicyKeepsTaskScopeAndPartialFailureRefresh(t *testing.T) {
	for _, kind := range []string{"task.archive", "task.restore", "task.delete", "task.materials-retry", "task.tools-launch", "codex.run"} {
		descriptor := DescribeAction(kind)
		if !descriptor.TaskMenuScoped || !descriptor.ReloadSnapshot {
			t.Errorf("task context policy incomplete: %s %+v", kind, descriptor)
		}
	}
	for _, kind := range []string{"task.delete", "task.tools-launch", "codex.run"} {
		if !DescribeAction(kind).RefreshOnError {
			t.Errorf("lost persisted partial result: %s", kind)
		}
	}
	for _, kind := range []string{"document.list", "document.read", "instructions.read"} {
		if !DescribeAction(kind).QuietSuccess {
			t.Errorf("read became noisy: %s", kind)
		}
	}
	unknown := DescribeAction("cli.extension/unregistered")
	if unknown.Known || unknown.TaskMenuScoped || unknown.ReloadSnapshot || unknown.Cancellable || unknown.Label != unknown.Kind {
		t.Fatalf("unsafe unknown policy: %+v", unknown)
	}
}

func TestRequirementLinkPolicyIsComplete(t *testing.T) {
	descriptor := DescribeAction("task.requirement-link")
	expected := ActionDescriptor{Kind: "task.requirement-link", Label: "关联需求", Known: true, TaskMenuScoped: true, ReloadSnapshot: true}
	if descriptor != expected {
		t.Fatalf("requirement link policy: got %+v, want %+v", descriptor, expected)
	}
}

func TestActionPolicyRetainsDynamicFeedbackPrefixes(t *testing.T) {
	if !DescribeAction("instructions.extension/unregistered").DedicatedResult {
		t.Fatal("instruction prefix feedback changed")
	}
	for _, kind := range []string{"config.extension", "migration.extension", "instructions.templates.extension", "skills.extension", "codex.extensions/new", "codex.ai/new", "cli.extension", "logs.extension"} {
		if !DescribeAction(kind).SettingsDedicatedResult {
			t.Errorf("settings feedback prefix changed: %s", kind)
		}
	}
	if DescribeAction("tool.extension").SettingsDedicatedResult {
		t.Fatal("unrelated feedback was suppressed")
	}
	for _, kind := range []string{"requirements.copy-document", "requirements.copy-comments"} {
		if !DescribeAction(kind).DedicatedResult {
			t.Errorf("reader feedback changed: %s", kind)
		}
	}
	if DescribeAction("requirements.metadata").DedicatedResult {
		t.Fatal("metadata feedback changed")
	}
}
