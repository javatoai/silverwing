package ui

// Split lays out first and second side by side, with a divider between
// them that the user drags to resize them, or moves with the arrows once
// it has the keyboard focus: *size is the width of first, which the
// divider keeps 40 DIPs from either edge. Changed reports a move. Right to
// left (Direction), first is on the right.
//
//	ui.Split(c, &app.sidebar, app.files, app.editor).Fill()
func coreSplit(c *context, size *float32, first, second func()) *node {
	return split(c, size, nil, first, second, false)
}

// SplitRatio is Split whose first pane is a fraction of the available width.
// Dragging changes the fraction; double-clicking restores an even split.
func coreSplitRatio(c *context, ratio *float32, first, second func()) *node {
	return split(c, nil, ratio, first, second, false)
}

// SplitVertical is Split with first above second, *size its height.
func coreSplitVertical(c *context, size *float32, first, second func()) *node {
	return split(c, size, nil, first, second, true)
}

func split(c *context, size, ratio *float32, first, second func(), vertical bool) *node {
	t := c.theme
	minPane, grip := t.Space(10), t.Space(1.5)
	if ratio != nil {
		minPane = t.Space(40)
	}
	if ratio != nil && (*ratio <= 0 || *ratio >= 1) {
		*ratio = .5
	}
	// One element or the other: creating one adds it to the parent.
	var e *node
	if vertical {
		e = coreColumn(c).AlignItems(Stretch)
	} else {
		e = coreRow(c).AlignItems(Stretch)
	}
	e.widget = "Split"
	e.Children(func() {
		// The panes meet at a line one DIP thick, with nothing between them
		// and it. The handle the pointer drags spans a few DIPs over either
		// pane, above them: it is placed in this box, which has no padding,
		// whatever the split's.
		var in *node
		if vertical {
			in = coreColumn(c).Grow(1).MinHeight(0).AlignItems(Stretch)
		} else {
			in = coreRow(c).Grow(1).MinWidth(0).AlignItems(Stretch)
		}
		// The room to share, as the last frame laid it out.
		total := in.st.w
		if vertical {
			total = in.st.h
		}
		if ratio != nil {
			in.Draw(func(_ *Painter, actual Rect) {
				available := actual.W
				if vertical {
					available = actual.H
				}
				if available <= 0 {
					return
				}
				minimum := min(minPane, max(float32(0), (available-1)/2))
				minRatio := minimum / available
				maxRatio := max(minRatio, (available-1-minimum)/available)
				clamped := min(max(*ratio, minRatio), maxRatio)
				if clamped != *ratio {
					*ratio = clamped
					c.AnimationFrame()
				}
			})
		}
		currentSize := float32(0)
		minRatio, maxRatio := float32(0), float32(1)
		paneMin := minPane
		if ratio != nil {
			if total > 0 {
				// Keep both panes usable when there is room, but let them
				// shrink evenly in windows narrower than two minimum panes.
				paneMin = min(minPane, max(float32(0), (total-1)/2))
				minRatio = paneMin / total
				maxRatio = max(minRatio, (total-1-paneMin)/total)
			}
			*ratio = min(max(*ratio, minRatio), maxRatio)
			currentSize = *ratio * total
		} else {
			currentSize = *size
		}
		set := func(v float32) {
			changed := false
			if ratio != nil {
				if total > 0 {
					v = min(max(v, paneMin), total-1-paneMin)
					v = min(max(v/total, minRatio), maxRatio)
				} else {
					v = min(max(v, .1), .9)
				}
				if v != *ratio {
					*ratio = v
					changed = true
				}
			} else {
				if total > 0 {
					v = min(v, total-1-minPane)
				}
				v = max(v, minPane)
				if v != *size {
					*size = v
					changed = true
				}
			}
			if changed {
				e.st.markChanged()
				c.rt.consumed = true
			}
		}
		in.Children(func() {
			a := coreBox(c).Shrink(0).Clip()
			if ratio != nil && vertical {
				a.HeightPercent(*ratio * 100)
			} else if ratio != nil {
				a.WidthPercent(*ratio * 100)
			} else if vertical {
				a.Height(*size)
			} else {
				a.Width(*size)
			}
			a.Children(first)

			// The line takes the focus between the panes, in their order.
			div := coreBox(c).Shrink(0).Focusable().Role(RoleSplitter).Label(c.texts().Divider())
			div.widget = "Divider"
			div.flags |= flagOwnRing
			if vertical {
				div.Height(1)
			} else {
				div.Width(1)
			}
			// The arrows move the line the way they point.
			back, forth := div.arrows()
			if vertical {
				back, forth = KeyUp, KeyDown
			}
			div.afterInput(func() {
				if div.Shortcut(0, back) {
					set(currentSize - t.Space(2.5))
				}
				if div.Shortcut(0, forth) {
					set(currentSize + t.Space(2.5))
				}
			})
			if ratio != nil {
				div.hasRange, div.accRange = true, [3]float64{float64(minRatio), float64(maxRatio), float64(*ratio)}
				div.accStep = float64(t.Space(2.5) / max(1, total))
			} else {
				div.hasRange, div.accRange = true, [3]float64{float64(minPane), float64(max(total-1-minPane, minPane)), float64(*size)}
				div.accStep = float64(t.Space(2.5))
			}

			b := coreBox(c).Grow(1).Shrink(1).Clip()
			if vertical {
				b.MinHeight(0)
			} else {
				b.MinWidth(0)
			}
			b.Children(second)

			handle := coreBox(c).Absolute().Label("Divider drag handle")
			handle.flags |= flagDraggable | flagHover
			if vertical {
				handle.Left(0).Right(0).Top(currentSize - (grip-1)/2).Height(grip).Cursor(CursorResizeNS)
				if ratio != nil {
					handle.TopPercent(*ratio*100).Translate(0, -(grip-1)/2)
				}
			} else {
				handle.Top(0).Bottom(0).Start(currentSize - (grip-1)/2).Width(grip).Cursor(CursorResizeEW)
				if ratio != nil {
					handle.StartPercent(*ratio*100).Translate(-(grip-1)/2, 0)
				}
			}
			// A press there focuses the line, as a press on it would.
			dx, dy, held := handle.Dragged()
			handle.afterInput(func() {
				if ratio != nil && handle.DoubleClicked() {
					if *ratio != .5 {
						*ratio = .5
						e.st.markChanged()
						c.rt.consumed = true
					}
					return
				}
				if held {
					div.Focus()
					if vertical {
						set(currentSize + dy)
					} else if handle.rtl() {
						set(currentSize - dx)
					} else {
						set(currentSize + dx)
					}
				}
			})
			div.Draw(func(p *Painter, r Rect) {
				line := t.Border
				if held || handle.Hovered() {
					line = t.Accent
				}
				p.Fill(r, line, 0)
			})
			handle.Draw(func(p *Painter, r Rect) {
				if div.FocusVisible() {
					p.FocusRing(r, [4]float32{})
				}
			})
		})
	})
	return e
}
