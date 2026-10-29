package desktop

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func validServiceCommandTestValues() serviceCommandValues {
	return serviceCommandValues{ID: "command", Name: "Build", Program: "go", Directory: ".", TimeoutText: "600", Enabled: true}
}

func TestServiceCommandValuesDirectoryPolicies(t *testing.T) {
	absolute := filepath.Join(t.TempDir(), "run")
	cases := []struct {
		name      string
		purpose   serviceCommandPurpose
		directory string
		wantError string
	}{
		{"module-relative", serviceCommandForModule, "sub/dir", ""},
		{"module-absolute", serviceCommandForModule, absolute, "快捷命令工作目录 必须使用仓库内相对路径：" + absolute},
		{"module-parent", serviceCommandForModule, `sub\..\other`, `快捷命令工作目录 不能包含 ..：sub\..\other`},
		{"module-git", serviceCommandForModule, "sub/.GiT/config", "快捷命令工作目录 不能访问 .git：sub/.GiT/config"},
		{"bootstrap-absolute", serviceCommandForBootstrap, absolute, ""},
		{"bootstrap-parent", serviceCommandForBootstrap, "../other", ""},
		{"bootstrap-git", serviceCommandForBootstrap, ".git/config", ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			values := validServiceCommandTestValues()
			values.Directory = test.directory
			timeout, err := validateServiceCommandValues(values, test.purpose)
			if test.wantError != "" {
				if err == nil || err.Error() != test.wantError {
					t.Fatalf("directory error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			command := serviceCommandConfig(values, test.purpose, timeout)
			if command["workingDirectory"] != test.directory {
				t.Fatalf("directory rewritten: %#v", command)
			}
		})
	}
}

func TestServiceCommandValuesRequiredFieldsAndErrorOrder(t *testing.T) {
	cases := []struct {
		name   string
		change func(*serviceCommandValues)
		want   string
	}{
		{"name-before-directory", func(v *serviceCommandValues) {
			v.Name, v.Program, v.Directory, v.ID, v.TimeoutText = " \t", "", "", "", "0"
		}, "命令名称和可执行程序不能为空"},
		{"program", func(v *serviceCommandValues) { v.Program = " \t" }, "命令名称和可执行程序不能为空"},
		{"directory-before-id", func(v *serviceCommandValues) { v.Directory, v.ID, v.TimeoutText = " \n", "", "0" }, "命令工作目录不能为空"},
		{"id-before-path", func(v *serviceCommandValues) { v.ID, v.Directory, v.TimeoutText = "", "../bad", "0" }, "快捷命令 ID 不能为空"},
		{"path-before-timeout", func(v *serviceCommandValues) { v.Directory, v.TimeoutText = "../bad", "0" }, "快捷命令工作目录 不能包含 ..：../bad"},
		{"original-name-in-error", func(v *serviceCommandValues) { v.Name, v.TimeoutText = " Build ", "0" }, "命令“ Build ”的超时必须是大于 0 的整数"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			values := validServiceCommandTestValues()
			test.change(&values)
			_, err := validateServiceCommandValues(values, serviceCommandForModule)
			if err == nil || err.Error() != test.want {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
	values := validServiceCommandTestValues()
	values.ID = ""
	if _, err := validateServiceCommandValues(values, serviceCommandForBootstrap); err != nil {
		t.Fatalf("bootstrap acquired module ID policy: %v", err)
	}
	values.ID = " "
	if _, err := validateServiceCommandValues(values, serviceCommandForModule); err != nil {
		t.Fatalf("module ID was newly trimmed: %v", err)
	}
}

func TestServiceCommandValuesPositiveInt64Timeout(t *testing.T) {
	for _, text := range []string{"", " \t", "0", "-1", "9223372036854775808", "1.5", "abc"} {
		t.Run("reject-"+text, func(t *testing.T) {
			values := validServiceCommandTestValues()
			values.TimeoutText = text
			_, err := validateServiceCommandValues(values, serviceCommandForModule)
			if err == nil || err.Error() != "命令“Build”的超时必须是大于 0 的整数" {
				t.Fatalf("invalid timeout %q: %v", text, err)
			}
		})
	}
	for text, want := range map[string]int64{"1": 1, "+1": 1, " 600 ": 600, "9223372036854775807": 9223372036854775807} {
		values := validServiceCommandTestValues()
		values.TimeoutText = text
		got, err := validateServiceCommandValues(values, serviceCommandForModule)
		if err != nil || got != want {
			t.Fatalf("timeout %q = %d, %v; want %d", text, got, err, want)
		}
	}
}

func TestServiceCommandValuesPreserveRawFieldsAndArgumentBytes(t *testing.T) {
	raw := map[string]any{"id": "old", "iconKey": "old-icon", "future": map[string]any{"mode": "kept", "nested": []any{"value"}}}
	before, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	values := serviceCommandValues{raw: raw, ID: "chosen", Name: " Build ", Program: " go ", Directory: " subdir ", TimeoutText: " 30 ", Enabled: false, IconKey: " terminal ", ArgsText: "\r\n \t\r  alpha  \r\nbeta\t\r   \n  quoted \"x y\"; $(echo)  \n"}
	timeout, err := validateServiceCommandValues(values, serviceCommandForModule)
	if err != nil {
		t.Fatal(err)
	}
	command := serviceCommandConfig(values, serviceCommandForModule, timeout)
	if command["name"] != values.Name || command["executable"] != values.Program || command["workingDirectory"] != values.Directory || command["iconKey"] != values.IconKey || command["id"] != values.ID || command["enabled"] != false || command["timeoutSeconds"] != int64(30) {
		t.Fatalf("form fields normalized or lost: %#v", command)
	}
	expectedArgs := []string{"  alpha  ", "beta\t", "  quoted \"x y\"; $(echo)  "}
	if !reflect.DeepEqual(command["arguments"], expectedArgs) {
		t.Fatalf("arguments = %#v, want %#v", command["arguments"], expectedArgs)
	}
	if !reflect.DeepEqual(command["future"], raw["future"]) {
		t.Fatalf("unknown raw field lost: %#v", command)
	}
	command["future"].(map[string]any)["mode"] = "changed in output"
	after, err := json.Marshal(raw)
	if err != nil || string(after) != string(before) {
		t.Fatalf("assembly aliased input map: before=%s after=%s err=%v", before, after, err)
	}
	bootstrap := serviceCommandConfig(values, serviceCommandForBootstrap, timeout)
	for _, key := range []string{"id", "iconKey"} {
		if _, exists := bootstrap[key]; exists {
			t.Fatalf("bootstrap retained %s: %#v", key, bootstrap)
		}
	}
	if bootstrap["enabled"] != false || !reflect.DeepEqual(bootstrap["future"], raw["future"]) {
		t.Fatalf("bootstrap changed retained fields: %#v", bootstrap)
	}
	after, _ = json.Marshal(raw)
	if string(after) != string(before) {
		t.Fatalf("bootstrap removal changed input: %s", after)
	}

	empty := validServiceCommandTestValues()
	parsed, err := validateServiceCommandValues(empty, serviceCommandForModule)
	if err != nil {
		t.Fatal(err)
	}
	defaulted := serviceCommandConfig(empty, serviceCommandForModule, parsed)
	if !reflect.DeepEqual(defaulted["arguments"], []string{}) || defaulted["iconKey"] != "build" {
		t.Fatalf("empty args/icon changed: %#v", defaulted)
	}
}

func newServiceCommandFormTestApp(commands []workspaceCommandDraft, bootstrap []bootstrapCommandDraft) *app {
	rawCommands := make([]any, 0, len(commands))
	for _, command := range commands {
		rawCommands = append(rawCommands, command.raw)
	}
	module := serviceModuleDraft{raw: map[string]any{"id": "default", "name": "default", "strategy": "STANDARD_WORKTREE", "tagMode": "MERGE_TO_TARGET_BRANCH", "customCommands": rawCommands}, id: "default", name: "default", strategy: "STANDARD_WORKTREE", tagMode: "MERGE_TO_TARGET_BRANCH", commands: commands}
	state := &serviceEditorState{session: "command-values", groupID: "group", serviceID: "service", modules: []serviceModuleDraft{module}, commands: bootstrap, commandsMode: "form", copiesMode: "form", initialText: map[string]string{}, initialBools: map[string]bool{}, service: map[string]any{"id": "service", "repositoryId": "repo", "displayName": "Service", "masterBranch": "origin/main", "testTagBaselineRef": "origin/release/test", "modules": []any{module.raw}}}
	state.initialModules, state.initialCopies, state.initialCommands = moduleIDs(state.modules), copyIDs(state.copies), bootstrapCommandIDs(state.commands)
	return &app{inputs: map[string]*string{}, workspaceUnit: workspaceController{state: workspaceState{serviceEditor: state}}}
}

func TestServiceCommandFormConsumersUseValuesAndKeepCacheBaselines(t *testing.T) {
	moduleRaw := map[string]any{"id": "build", "name": " Build ", "executable": "go", "arguments": []any{"original"}, "enabled": false, "future": "module-retained"}
	module := workspaceCommandFromMap(moduleRaw)
	bootstrapRaw := map[string]any{"id": "legacy-id", "iconKey": "legacy-icon", "name": "Prepare", "executable": "go", "arguments": []any{"", " keep "}, "workingDirectory": filepath.Join(t.TempDir(), "run"), "enabled": false, "future": "bootstrap-retained"}
	bootstrap := bootstrapCommandDrafts([]any{bootstrapRaw})
	s := newServiceCommandFormTestApp([]workspaceCommandDraft{module}, bootstrap)
	state := s.workspaceUnit.state.serviceEditor
	before, _ := json.Marshal(state.service)
	owner := "module:default"
	timeoutInput := s.serviceCommandText(owner, &module, "timeout", module.timeout)
	*timeoutInput = " 7200 "
	argsInput := s.serviceCommandText(owner, &module, "arguments", module.arguments)
	*argsInput = "  --module  \r\n\r  literal && shell \t"
	enabledInput := s.serviceEditorBool(owner+":"+module.id+":enabled", false)
	*enabledInput = true
	service, err := s.buildServiceEditorConfig()
	if err != nil {
		t.Fatal(err)
	}
	moduleConfig := service["modules"].([]any)[0].(map[string]any)
	command := moduleConfig["customCommands"].([]any)[0].(map[string]any)
	if command["timeoutSeconds"] != int64(7200) || command["enabled"] != true || command["future"] != "module-retained" || !reflect.DeepEqual(command["arguments"], []string{"  --module  ", "  literal && shell \t"}) {
		t.Fatalf("module consumer bypassed captured values: %#v", command)
	}
	bootstrapConfig := service["bootstrap"].(map[string]any)["commands"].([]any)[0].(map[string]any)
	if bootstrapConfig["timeoutSeconds"] != int64(600) || bootstrapConfig["enabled"] != false || bootstrapConfig["workingDirectory"] != bootstrapRaw["workingDirectory"] || bootstrapConfig["future"] != "bootstrap-retained" || !reflect.DeepEqual(bootstrapConfig["arguments"], []string{" keep "}) {
		t.Fatalf("bootstrap consumer bypassed value policy: %#v", bootstrapConfig)
	}
	for _, key := range []string{"id", "iconKey"} {
		if _, exists := bootstrapConfig[key]; exists {
			t.Fatalf("bootstrap payload retained %s: %#v", key, bootstrapConfig)
		}
	}
	timeoutKey := s.serviceEditorKey(owner + ":" + module.id + ":timeout")
	if state.initialText[timeoutKey] != "1800" || *timeoutInput != " 7200 " || state.initialBools[s.serviceEditorKey(owner+":"+module.id+":enabled")] != false {
		t.Fatalf("cached edits/default baseline changed: timeout=%q initial=%q bools=%#v", *timeoutInput, state.initialText[timeoutKey], state.initialBools)
	}
	bootstrapTimeoutKey := s.serviceEditorKey("bootstrap:" + bootstrap[0].id + ":timeout")
	if state.initialText[bootstrapTimeoutKey] != "600" || !s.serviceEditorDirty() {
		t.Fatalf("bootstrap default or edited-state tracking changed: %#v", state.initialText)
	}
	after, _ := json.Marshal(state.service)
	if string(before) != string(after) || s.win != nil || s.backend != nil {
		t.Fatalf("payload build changed source or required runtime: before=%s after=%s", before, after)
	}
}

func TestServiceCommandFormFailureRetainsLazyInputInitialization(t *testing.T) {
	draft := workspaceCommandFromMap(map[string]any{"id": "build", "name": "", "executable": "go"})
	s := newServiceCommandFormTestApp([]workspaceCommandDraft{draft}, nil)
	state := s.workspaceUnit.state.serviceEditor
	owner := "module:default"
	if _, err := s.buildWorkspaceCommand(owner, draft, true); err == nil || err.Error() != "命令名称和可执行程序不能为空" {
		t.Fatalf("required-field error changed: %v", err)
	}
	if len(s.inputs) != 4 || len(state.initialText) != 4 || len(state.initialBools) != 0 {
		t.Fatalf("failed validation initialized deferred inputs: inputs=%#v text=%#v bools=%#v", s.inputs, state.initialText, state.initialBools)
	}
	for _, field := range []string{"arguments", "iconKey"} {
		if _, exists := s.inputs[s.serviceEditorKey(owner+":"+draft.id+":"+field)]; exists {
			t.Fatalf("failed validation initialized %s", field)
		}
	}
	*s.serviceCommandText(owner, &draft, "name", draft.name) = "Ready"
	command, err := s.buildWorkspaceCommand(owner, draft, true)
	if err != nil || command["name"] != "Ready" || command["timeoutSeconds"] != int64(1800) {
		t.Fatalf("cached correction did not reach payload: %#v %v", command, err)
	}
	if state.initialText[s.serviceEditorKey(owner+":"+draft.id+":name")] != "" || len(state.initialBools) != 1 || !s.serviceEditorDirty() {
		t.Fatalf("correction replaced initial baseline: text=%#v bools=%#v", state.initialText, state.initialBools)
	}
}
