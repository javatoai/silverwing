package desktop

import (
	"fmt"
	"strings"

	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

type serviceModuleTagDraft struct {
	Enabled       bool
	Mode          string
	InheritTarget bool
	Target        string
	MessagePrefix string
}

func (s *app) readServiceModuleTagDraft(module *serviceModuleDraft) serviceModuleTagDraft {
	key := "module:" + module.id + ":"
	text := func(field, initial string) string {
		if value := s.inputs[s.serviceEditorKey(key+field)]; value != nil {
			return *value
		}
		return initial
	}
	flag := func(field string, initial bool) bool {
		if value := s.settingsUnit.state.settingsBoolInputs[s.serviceEditorKey(key+field)]; value != nil {
			return *value
		}
		return initial
	}
	return serviceModuleTagDraft{
		Enabled:       flag("tagEnabled", mapBool(module.raw, "tagEnabled", module.strategy == "STANDARD_WORKTREE")),
		Mode:          text("tagMode", module.tagMode),
		InheritTarget: flag("inherit-tag-target", module.tagTargetRef == ""),
		Target:        text("tagTargetRef", module.tagTargetRef),
		MessagePrefix: text("tagMessagePrefix", module.tagMessagePrefix),
	}
}

func (d serviceModuleTagDraft) targetForSave() (*string, error) {
	switch domain.TagBuildMode(d.Mode) {
	case domain.TagBuildCurrentBranch:
		return nil, nil
	case domain.TagBuildMergeToTargetBranch:
		if d.InheritTarget {
			return nil, nil
		}
		target := strings.TrimSpace(d.Target)
		if !validRemoteBranch(target) {
			return nil, fmt.Errorf("Tag 目标必须是 remote/branch 格式")
		}
		if _, err := domain.ParseRemoteBranchRef(target); err != nil {
			return nil, fmt.Errorf("Tag 目标分支：%w", err)
		}
		return &target, nil
	default:
		return nil, fmt.Errorf("Tag 模式无效")
	}
}

func (s *app) applyServiceModuleTagDraft(module *serviceModuleDraft, draft serviceModuleTagDraft) {
	key := "module:" + module.id + ":"
	*s.serviceEditorBool(key+"tagEnabled", mapBool(module.raw, "tagEnabled", module.strategy == "STANDARD_WORKTREE")) = draft.Enabled
	*s.serviceEditorBool(key+"inherit-tag-target", module.tagTargetRef == "") = draft.InheritTarget
	*s.serviceEditorModuleText(module, "tagMode", module.tagMode) = draft.Mode
	*s.serviceEditorModuleText(module, "tagTargetRef", module.tagTargetRef) = draft.Target
	*s.serviceEditorModuleText(module, "tagMessagePrefix", module.tagMessagePrefix) = draft.MessagePrefix
}

func (s *app) renderServiceModuleTagPopup(c *ui.Context, p palette, state *serviceEditorState, popup *serviceModulePopup) {
	draft := &popup.tag
	switchRow := ui.Row(c.Key(popup.key+"-tag-enabled-row")).FillWidth().Gap(8).AlignItems(ui.Center).Padding(3, 0)
	showDisabledDetails := ui.Local(switchRow, "show-disabled-details", func() bool { return false })
	switchRow.Children(func() {
		label(c, "启用测试 Tag", 12, p.text).Grow(1)
		ui.Switch(c.Key(popup.key+"-tag-enabled"), &draft.Enabled).Label("启用测试 Tag")
	})
	// Keep correcting fields visible while the user finishes editing a rejected draft.
	*showDisabledDetails = !draft.Enabled && (*showDisabledDetails || popup.err != "")
	if !draft.Enabled && !*showDisabledDetails {
		return
	}

	ui.Column(c).Gap(6).Children(func() {
		label(c, "Tag 模式", 11, p.muted)
		ui.Row(c).FillWidth().Wrap().Gap(6).Children(func() {
			for _, choice := range []struct {
				mode  domain.TagBuildMode
				title string
			}{
				{domain.TagBuildMergeToTargetBranch, "合并到目标分支后构建"},
				{domain.TagBuildCurrentBranch, "当前分支直接构建"},
			} {
				mode := string(choice.mode)
				s.settingsChoice(c, p, popup.key+"-tag-mode-"+mode, choice.title, draft.Mode == mode, func() { draft.Mode = mode })
			}
		})
	})
	if draft.Mode == string(domain.TagBuildMergeToTargetBranch) {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			label(c, "继承服务 Tag 目标", 11, p.text).Grow(1)
			ui.Switch(c.Key(popup.key+"-tag-inherit"), &draft.InheritTarget).Label("继承服务 Tag 目标")
		})
		if draft.InheritTarget {
			target := mapString(state.service, "testTagBaselineRef")
			if edited := s.inputs[s.serviceEditorKey("testTagBaselineRef")]; edited != nil {
				target = *edited
			}
			label(c, "当前继承："+target, 10, p.muted)
		} else {
			s.renderServiceEditorBranchInput(c, p, state, &draft.Target, "Tag 目标分支", popup.key+"-tag-target")
		}
	}
	ui.Column(c).Gap(4).Children(func() {
		const title = "Tag 信息前缀"
		label(c, title, 11, p.muted)
		ui.TextInput(c.Key(popup.key+"-tag-prefix"), &draft.MessagePrefix).FillWidth().Height(36).Placeholder(title).Label(title)
	})
	if popup.err != "" {
		if _, err := draft.targetForSave(); err == nil {
			popup.err = ""
		}
	}
}
