package desktop

import (
	"sync"

	"github.com/egoist/mygo/ui"
)

const (
	taskListPaneMinWidth   = float32(160)
	taskDetailPaneMinWidth = float32(600)
	taskListResizeWidth    = float32(8)
)

type taskListPaneLayout struct {
	width    float32
	dragging bool
}

var taskListPaneLayouts = struct {
	sync.Mutex
	byApp map[*app]*taskListPaneLayout
}{byApp: make(map[*app]*taskListPaneLayout)}

func (s *app) taskListPaneSplit(c *ui.Context, availableWidth float32, p palette, first, second func()) {
	layout := s.taskListPaneLayout()
	if !layout.dragging {
		layout.width = clampTaskListPaneWidth(s.listWidth, availableWidth)
	}

	row := ui.Row(c).Grow(1).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch)
	row.Children(func() {
		ui.Box(c).Width(layout.width).Shrink(0).MinWidth(0).MinHeight(0).Clip().Children(first)

		handle := ui.Box(c.Key("task-list-pane-resize")).Width(taskListResizeWidth).FillHeight().
			Cursor(ui.CursorResizeEW).Focusable().Role(ui.RoleSplitter).Label("拖动调整任务列表宽度").Tooltip("拖动调整任务列表宽度")
		dx, _, held := handle.Dragged()
		if held {
			layout.width = clampTaskListPaneWidth(layout.width+dx, availableWidth)
			layout.dragging = true
		} else if layout.dragging {
			layout.width = clampTaskListPaneWidth(layout.width, availableWidth)
			layout.dragging = false
			// Keep the preferred width unchanged during window clamping and
			// commit only when the user releases the divider.
			s.listWidth = layout.width
		}
		line := p.line
		if held || handle.Hovered() {
			line = p.blue
		}
		handle.Center().Children(func() {
			ui.Box(c).Width(2).FillHeight().Margin(16, 0).Background(line)
		})

		ui.Box(c).Grow(1).MinWidth(0).MinHeight(0).Clip().Children(second)
	})
}

func (s *app) taskListPaneLayout() *taskListPaneLayout {
	taskListPaneLayouts.Lock()
	defer taskListPaneLayouts.Unlock()
	if layout := taskListPaneLayouts.byApp[s]; layout != nil {
		return layout
	}
	layout := &taskListPaneLayout{width: s.listWidth}
	taskListPaneLayouts.byApp[s] = layout
	return layout
}

func clampTaskListPaneWidth(preferredWidth, availableWidth float32) float32 {
	maximum := max(taskListPaneMinWidth,
		availableWidth-taskListResizeWidth-taskDetailPaneMinWidth)
	return max(taskListPaneMinWidth, min(preferredWidth, maximum))
}
