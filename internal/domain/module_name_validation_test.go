package domain

import (
	"strings"
	"testing"
)

func TestValidateModuleName(t *testing.T) {
	for _, name := range []string{
		"default",
		"api-v2",
		"foo_bar",
		"service/module",
		"v4.1.5",
		"api.v1/worker-v2.0",
		"a.locked",
	} {
		t.Run("accept/"+name, func(t *testing.T) {
			if err := ValidateModuleName(name); err != nil {
				t.Fatalf("ValidateModuleName(%q) error = %v", name, err)
			}
		})
	}

	for _, name := range []string{
		"",
		".",
		"..",
		".hidden",
		"api.",
		"api..v1",
		"api.lock",
		"api.LOCK",
		"api/v1.lock",
		"/api",
		"api/",
		"api//worker",
		"api name",
		"中文",
		"api\\worker",
		"api:worker",
	} {
		t.Run("reject/"+strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			if err := ValidateModuleName(name); err == nil {
				t.Fatalf("ValidateModuleName(%q) unexpectedly succeeded", name)
			}
		})
	}
}

func TestStandardWorktreeModuleNamesAllowsDotsAndPreservesDuplicateChecks(t *testing.T) {
	modules := []ServiceModuleConfig{{ID: "version", Name: "v4.1.5"}, {ID: "api", Name: "api.v2"}}
	names, err := StandardWorktreeModuleNames(modules)
	if err != nil {
		t.Fatal(err)
	}
	if names["version"] != "v4.1.5" || names["api"] != "api.v2" {
		t.Fatalf("module names were not preserved: %#v", names)
	}
	defaults, err := StandardWorktreeModuleNames([]ServiceModuleConfig{{ID: "blank", Name: "  "}})
	if err != nil || defaults["blank"] != "default" {
		t.Fatalf("blank module name no longer falls back to default: %#v %v", defaults, err)
	}

	for _, modules := range [][]ServiceModuleConfig{
		{{ID: "one", Name: "Api.v1"}, {ID: "two", Name: "api.V1"}},
		{{ID: "one", Name: "api/service"}, {ID: "two", Name: "api-service"}},
	} {
		if _, err := StandardWorktreeModuleNames(modules); err == nil {
			t.Fatalf("duplicate modules unexpectedly passed validation: %#v", modules)
		}
	}
}

func TestTaskBranchNamesPreservesDottedModuleSuffix(t *testing.T) {
	modules := []ServiceModuleConfig{{ID: "version", Name: "v4.1.5"}, {ID: "api", Name: "api.v2"}}
	branches, err := TaskBranchNames("feature/task", modules, nil)
	if err != nil {
		t.Fatal(err)
	}
	if branches["version"] != "feature/task-v4.1.5" || branches["api"] != "feature/task-api.v2" {
		t.Fatalf("unexpected module branches: %#v", branches)
	}
}
