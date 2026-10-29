package desktop

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

type confirmationDialogButton struct {
	label   string
	primary bool
}

type confirmationDialogField struct {
	Label, Value string
}

type confirmationDialogState struct {
	Title        string
	Message      string
	Detail       string
	Summary      []confirmationDialogField
	Notices      []string
	FoldDetail   bool
	DetailOpen   bool
	Current      func() bool
	Buttons      []confirmationDialogButton
	DefaultIndex int
	CancelIndex  int
	OwnerKey     string
	Result       func(int)
	Open         bool
}

func (s *app) showConfirmationDialog(title, message, detail string, buttons []confirmationDialogButton, defaultIndex, cancelIndex int, result func(int)) bool {
	if s == nil || s.settingsUnit.state.settingsPickerBusy || s.win == nil || len(buttons) == 0 ||
		defaultIndex < 0 || defaultIndex >= len(buttons) || cancelIndex < 0 || cancelIndex >= len(buttons) {
		return false
	}
	s.settingsUnit.state.settingsPickerBusy = true
	s.confirmationDialog = &confirmationDialogState{
		Title:        title,
		Message:      message,
		Detail:       detail,
		Buttons:      append([]confirmationDialogButton(nil), buttons...),
		DefaultIndex: defaultIndex,
		CancelIndex:  cancelIndex,
		OwnerKey:     s.confirmationOwnerKey(),
		Result:       result,
		Open:         true,
	}
	s.win.Invalidate()
	return true
}

func (s *app) confirmationOwnerKey() string {
	if s == nil {
		return ""
	}
	t := s.taskUnit.currentTask()
	return strings.Join([]string{
		s.page,
		strconv.Itoa(s.tab),
		t.id,
		t.path,
		t.createdAt,
		s.materialsUnit.state.documentPath,
		s.settingsUnit.state.settingsPage,
		s.settingsUnit.state.settingsTab,
		s.settingsUnit.state.settingsGroupID,
	}, "\x00")
}

func (s *app) renderConfirmationDialog(c *ui.Context, p palette) {
	state := s.confirmationDialog
	if state == nil {
		return
	}
	if state.OwnerKey != s.confirmationOwnerKey() || state.Current != nil && !state.Current() {
		s.finishConfirmationDialog(state, state.CancelIndex)
		return
	}
	dialog := ui.DialogBase(c.Key("confirmation-dialog"), &state.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .38))
		width, height := c.Size()
		maxHeight := max(float32(1), min(float32(560), height-32))
		panel.Width(max(1, min(500, width-32))).MaxHeight(maxHeight).Padding(20).Gap(14).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() {
			label(c, state.Title, 18, p.text).FontWeight(600).Wrap().Shrink(0)
			ui.Scroll(c).FillWidth().MaxHeight(max(1, maxHeight-180)).MinHeight(0).Children(func() {
				ui.Column(c).FillWidth().MinWidth(0).Gap(10).Children(func() {
					if state.Message != "" {
						label(c, state.Message, 13, p.text).Wrap()
					}
					for _, field := range state.Summary {
						ui.Row(c).FillWidth().MinWidth(0).Gap(10).AlignItems(ui.Start).Children(func() {
							label(c, field.Label, 12, p.muted).Width(48).Shrink(0)
							label(c, field.Value, 12, p.text).Grow(1).MinWidth(0).Wrap().Selectable()
						})
					}
					for _, notice := range state.Notices {
						label(c, notice, 12, p.text).Wrap()
					}
					if state.Detail != "" && state.FoldDetail {
						details := ui.CollapsibleBase(c.Key("confirmation-details"), &state.DetailOpen)
						details.Trigger.AlignSelf(ui.Start).Height(28).Padding(0, 4).Gap(6).AlignItems(ui.Center).Label("详细信息").Children(func() {
							icon(c, map[bool]string{true: "chevron", false: "chevron-right"}[state.DetailOpen], 13, p.muted)
							label(c, "详细信息", 12, p.text).NoWrap()
						})
						details.Panel(func() {
							label(c, state.Detail, 11, p.muted).FillWidth().MinWidth(0).Wrap().Selectable()
						}).FillWidth().MinWidth(0).Padding(10).Radius(8).Background(p.soft)
					} else if state.Detail != "" {
						ui.Box(c).FillWidth().Padding(10).Radius(8).Background(p.soft).Children(func() {
							label(c, state.Detail, 11, p.muted).Wrap().Selectable()
						})
					}
				})
			})
			buttons := ui.Row(c).Gap(8).Justify(ui.End).Shrink(0)
			if width < 460 {
				buttons.Column().AlignItems(ui.Stretch)
			}
			buttons.Children(func() {
				for index, button := range state.Buttons {
					if confirmationDialogButtonUI(c, fmt.Sprintf("confirmation-button-%d", index), button, index == state.DefaultIndex, p) {
						s.finishConfirmationDialog(state, index)
					}
				}
			})
		})
	})
	if dialog.Dismissed() {
		s.finishConfirmationDialog(state, state.CancelIndex)
	}
}

func confirmationDialogButtonUI(c *ui.Context, key string, button confirmationDialogButton, autoFocus bool, p palette) bool {
	name := button.label
	element := ui.ButtonBase(c.Key(key)).Row().Height(34).Padding(0, 16).Radius(18).AlignItems(ui.Center).Label(name)
	if autoFocus {
		element.AutoFocus()
	}
	background, foreground := p.surface, p.text
	if button.primary {
		background, foreground = p.blue, ui.Hex("#FFFFFF")
	}
	if element.Hovered() {
		if button.primary {
			background = ui.Hex("#285CD3")
		} else {
			background = p.soft
		}
	}
	if !button.primary {
		element.Border(1, p.line)
	}
	if element.Focused() {
		element.Border(2, p.blue)
	}
	element.Background(background).Children(func() {
		label(c, name, 13, foreground).NoWrap()
	})
	return element.Clicked()
}

func (s *app) finishConfirmationDialog(state *confirmationDialogState, index int) {
	if s == nil || state == nil || s.confirmationDialog != state {
		return
	}
	if index != state.CancelIndex && state.Current != nil && !state.Current() {
		index = state.CancelIndex
	}
	state.Open = false
	result := state.Result
	s.confirmationDialog = nil
	s.settingsUnit.state.settingsPickerBusy = false
	if result != nil && index >= 0 && index < len(state.Buttons) {
		result(index)
	}
}

// cancelConfirmationDialog drops a pending confirmation during window close
// without running a destructive or external-write continuation.
func (s *app) cancelConfirmationDialog() {
	if s == nil || s.confirmationDialog == nil {
		return
	}
	state := s.confirmationDialog
	state.Open = false
	s.confirmationDialog = nil
	s.settingsUnit.state.settingsPickerBusy = false
}
