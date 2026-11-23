package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

type serviceModulePopupKind uint8

const (
	serviceModulePopupTag serviceModulePopupKind = iota
	serviceModulePopupCommands
)

type serviceModulePopup struct {
	key, session, moduleID string
	kind                   serviceModulePopupKind
	err                    string
	tag                    serviceModuleTagDraft
	commands               serviceModuleCommandsDraft
}

func (s *app) renderServiceModulePopupButtons(c *ui.Context, p palette, state *serviceEditorState, module *serviceModuleDraft) {
	ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
		if s.configBool("tagEnabled", false) {
			status := "已关闭"
			if s.readServiceModuleTagDraft(module).Enabled {
				status = "已启用"
			}
			if action(c, "service-editor-module-tag-"+module.id, "tag", "测试 Tag · "+status, p, false) {
				s.openServiceModulePopup(state, module, serviceModulePopupTag)
			}
		}
		if action(c, "service-editor-module-commands-"+module.id, "terminal", fmt.Sprintf("快捷命令 · %d 条", len(module.commands)), p, false) {
			s.openServiceModulePopup(state, module, serviceModulePopupCommands)
		}
	})
}

func (s *app) openServiceModulePopup(state *serviceEditorState, module *serviceModuleDraft, kind serviceModulePopupKind) {
	if state == nil || module == nil || state.modulePopup != nil {
		return
	}
	popup := &serviceModulePopup{session: state.session, moduleID: module.id, kind: kind}
	owner := s.serviceModulePopupOwner(state, popup)
	if owner == nil {
		return
	}
	switch kind {
	case serviceModulePopupTag:
		popup.tag = s.readServiceModuleTagDraft(owner)
	case serviceModulePopupCommands:
		popup.commands = s.readServiceModuleCommandsDraft(owner)
	default:
		return
	}
	popup.key = newDesktopEditorID("service-editor-module-popup")
	state.modulePopup = popup
}

func (s *app) serviceModulePopupOwner(state *serviceEditorState, popup *serviceModulePopup) *serviceModuleDraft {
	if state == nil || state != s.workspaceUnit.state.serviceEditor || !s.dialogOpen ||
		state.saving || state.section != "modules" || state.session != popup.session {
		return nil
	}
	if popup.kind == serviceModulePopupTag && !s.configBool("tagEnabled", false) {
		return nil
	}
	return findServiceModuleDraft(state.modules, popup.moduleID)
}

func (s *app) completeServiceModulePopup(state *serviceEditorState, popup *serviceModulePopup) {
	if state == nil || popup == nil || state.modulePopup != popup {
		return
	}
	module := s.serviceModulePopupOwner(state, popup)
	if module == nil {
		state.modulePopup = nil
		return
	}
	var err error
	switch popup.kind {
	case serviceModulePopupTag:
		_, err = popup.tag.targetForSave()
		if err == nil {
			s.applyServiceModuleTagDraft(module, popup.tag)
		}
	case serviceModulePopupCommands:
		err = s.applyServiceModuleCommandsDraft(module, &popup.commands)
	}
	if err != nil {
		popup.err = err.Error()
		return
	}
	state.modulePopup = nil
}

func (s *app) renderServiceModulePopup(c *ui.Context, p palette, state *serviceEditorState) {
	if state == nil || state.modulePopup == nil {
		return
	}
	popup := state.modulePopup
	module := s.serviceModulePopupOwner(state, popup)
	if module == nil {
		state.modulePopup = nil
		return
	}
	moduleName := firstNonEmpty(module.name, module.id)
	if edited := s.inputs[s.serviceEditorKey("module:"+module.id+":name")]; edited != nil {
		moduleName = firstNonEmpty(*edited, "default")
	}
	title := "测试 Tag · " + moduleName
	maxWidth, maxHeight := float32(520), float32(420)
	bodyKey := popup.key + "-body"
	if popup.kind == serviceModulePopupCommands {
		title = "快捷命令 · " + moduleName
		maxWidth, maxHeight = 620, 560
		bodyKey = fmt.Sprintf("%s-%d", bodyKey, popup.commands.selected)
	}
	width, height := c.Size()
	inset, padding, gap := float32(16), float32(18), float32(10)
	if width < 480 || height < 420 {
		inset, padding, gap = 8, 12, 8
	}
	open := true
	dialog := ui.DialogBase(c.Key(popup.key), &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.36))
		panel.Label(title).Width(max(1, min(maxWidth, width-2*inset))).MaxHeight(max(1, min(maxHeight, height-2*inset))).
			Padding(padding).Gap(gap).Background(p.surface).Border(1, p.line).Radius(14)
		panel.Children(func() {
			label(c, title, 17, p.text).FontWeight(600).SingleLine().Tooltip(title)
			ui.Scroll(c.Key(bodyKey)).FillWidth().Grow(1).MinHeight(0).Padding(2).Gap(10).Label("模块配置内容").Children(func() {
				if popup.kind == serviceModulePopupTag {
					s.renderServiceModuleTagPopup(c, p, state, popup)
				} else {
					s.renderServiceModuleCommandsPopup(c, p, popup)
				}
			})
			if popup.err != "" {
				label(c, popup.err, 12, p.danger).Wrap().MaxHeight(46).Tooltip(popup.err)
			}
			ui.Divider(c)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if action(c, popup.key+"-cancel", "", "取消", p, false) {
					state.modulePopup = nil
				}
				if action(c, popup.key+"-complete", "", "完成", p, true) {
					s.completeServiceModulePopup(state, popup)
					c.Invalidate()
				}
			})
		})
	})
	if dialog.Dismissed() {
		state.modulePopup = nil
	}
	if state.modulePopup != popup {
		c.Invalidate()
	}
}
