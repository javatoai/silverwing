package desktop

import "github.com/egoist/mygo/ui"

type taskServiceChoiceRowProps struct {
	key, name, detail string
	buttonLabel       string
	selected, active  bool
	disabled          bool
	onClick           func()
	onSelectionChange func(bool)
}

// renderTaskServiceChoiceRow keeps inclusion and editor focus as separate
// controls. Callers may still choose their own row-click semantics.
func renderTaskServiceChoiceRow(c *ui.Context, p palette, props taskServiceChoiceRowProps) {
	buttonLabel := props.buttonLabel
	if buttonLabel == "" {
		buttonLabel = props.name
	}
	background := p.surface
	border := p.line
	if props.selected {
		background = p.soft
	}
	if props.active {
		background = p.selected
		border = p.blue
	}

	ui.Row(c.Key(props.key+"-row")).FillWidth().MinWidth(0).Gap(5).AlignItems(ui.Center).
		Padding(4, 6).Background(background).Border(1, border).Radius(9).Children(func() {
		checked := props.selected
		check := ui.Checkbox(c.Key(props.key+"-include"), &checked, "").Shrink(0).
			Disabled(props.disabled).Label("选择服务：" + props.name)
		if check.Changed() && props.onSelectionChange != nil {
			props.onSelectionChange(checked)
		}

		button := ui.ButtonBase(c.Key(props.key+"-focus")).Row().Grow(1).MinWidth(0).
			Height(46).Padding(4, 5).Radius(7).Background(background).
			Label(buttonLabel).Tooltip(props.name).Disabled(props.disabled).FocusRing(true)
		button.Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				label(c, props.name, 12, p.text).FontWeight(600).FillWidth().SingleLine().Tooltip(props.name)
				if props.detail != "" {
					label(c, props.detail, 10, p.muted).FillWidth().SingleLine().Tooltip(props.detail)
				}
			})
			if props.active {
				label(c, "正在设置", 10, p.blue).Shrink(0).NoWrap()
			}
		})
		if button.Clicked() && props.onClick != nil {
			props.onClick()
		}
	})
}
