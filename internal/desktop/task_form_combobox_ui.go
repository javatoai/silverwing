package desktop

import "github.com/egoist/mygo/ui"

func drawTaskComboboxChevron(input ui.Element, p palette) {
	input.Draw(func(draw *ui.Painter, rect ui.Rect) {
		var arrow ui.Path
		arrow.MoveTo(rect.X+rect.W-21, rect.Y+15).LineTo(rect.X+rect.W-17, rect.Y+19).LineTo(rect.X+rect.W-13, rect.Y+15)
		draw.StrokePath(&arrow, 1.5, p.muted)
	})
}
