package markdownui

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	md "github.com/egoist/mygo/ui/markdown"
)

type imageEdit struct {
	open, importing          bool
	info                     md.ImageInfo
	documentID, documentPath string
	target, alt, errorText   string
	position                 ui.Rect
	focus                    bool
	cancel                   context.CancelFunc
}

func pendingImageInput(edit *imageEdit) bool {
	return edit != nil && (edit.importing || edit.errorText != "" || edit.target != edit.info.Destination || edit.alt != edit.info.Alt)
}

func (s *EditorState) editImage(info md.ImageInfo, position ui.Rect) {
	if _, err := s.CaptureInput(); err != nil {
		s.report(err)
		return
	}
	if s.options.ReadOnly || s.Mode() != Live {
		return
	}
	s.image = &imageEdit{open: true, focus: true, info: info, documentID: s.document.ID, documentPath: s.document.Path, target: info.Destination, alt: info.Alt, position: position}
}

func (s *EditorState) imageMenu(menu *ui.Menu) {
	if s.options.ReadOnly || s.Mode() != Live {
		return
	}
	info, ok := s.native.SelectedImage()
	if !ok {
		return
	}
	menu.Separator()
	if menu.Item("编辑图片链接").Chosen() {
		s.editImage(info, ui.Rect{})
	}
	if menu.Item("查看原图").Chosen() {
		s.openImage(info)
	}
	if menu.Item("删除图片").Chosen() {
		s.report(s.native.DeleteImage(info))
		s.notify()
	}
}

func (s *EditorState) openImage(info md.ImageInfo) {
	if native := s.options.Native; native != nil && native.OnImageOpen != nil {
		native.OnImageOpen(info)
		return
	}
	if target, err := ResolveLink(s.document.Path, info.Destination); err == nil && s.options.OnNavigate != nil {
		s.options.OnNavigate(target)
	}
}

func (s *EditorState) imageActions(c *ui.Context, info md.ImageInfo, anchor ui.Element) {
	if s.image != nil || s.closed || s.options.ReadOnly || s.Mode() != Live {
		return
	}
	ui.Overlay(c, func() {
		bounds := anchor.Bounds()
		width, height := c.Size()
		y := bounds.Y - 40
		if y < 4 {
			y = min(height-40, bounds.Y+bounds.H+4)
		}
		row := ui.Row(c.Key(fmt.Sprintf("image-actions-%d", info.Start))).AttachTo(anchor, ui.AnchorTopStart, ui.AnchorTopStart).Left(max(4, min(bounds.X, width-300))-bounds.X).Top(max(4, y)-bounds.Y).KeepFocus().Gap(4).Padding(4).Radius(6).Background(c.Theme().Background).Border(1, c.Theme().Border)
		row.HandleInput(func(event ui.InputEvent) bool { return false })
		row.Children(func() {
			s.native.ImageMoveHandle(c, info)
			if ui.Button(c, "编辑").Clicked() {
				s.editImage(info, bounds)
			}
			if ui.Button(c, "查看原图").Clicked() {
				s.openImage(info)
			}
			ui.PopoverMenuButton(c, "更多", func(menu *ui.Menu) {
				if menu.Item("在前面插入段落").Chosen() {
					s.native.ImageParagraph(info, false)
				}
				if menu.Item("在后面插入段落").Chosen() {
					s.native.ImageParagraph(info, true)
				}
				if menu.Item("上移图片 · Alt+↑").Chosen() {
					s.report(s.native.MoveImage(info, -1))
					s.notify()
				}
				if menu.Item("下移图片 · Alt+↓").Chosen() {
					s.report(s.native.MoveImage(info, 1))
					s.notify()
				}
				menu.Separator()
				if menu.Item("删除图片").Chosen() {
					s.report(s.native.DeleteImage(info))
					s.native.Focus()
					s.notify()
				}
			})
		})
	})
}

func (s *EditorState) imageDialogCurrent(edit *imageEdit) bool {
	return edit != nil && s.image == edit && !s.closed && !s.options.ReadOnly && s.Mode() == Live && s.document.ID == edit.documentID && s.document.Path == edit.documentPath && s.Snapshot().Version == edit.info.Version && !s.native.Composing()
}

func (s *EditorState) cancelImageEdit() {
	if s.image != nil && s.image.cancel != nil {
		s.image.cancel()
	}
	s.image = nil
	s.native.Focus()
}

func (s *EditorState) applyImageEdit(edit *imageEdit) {
	if !s.imageDialogCurrent(edit) {
		edit.errorText = ErrStale.Error()
		return
	}
	target := strings.TrimSpace(edit.target)
	if target == "" {
		edit.errorText = "请输入图片地址"
		return
	}
	if target == edit.info.Destination && edit.alt == edit.info.Alt {
		s.cancelImageEdit()
		return
	}
	resolved, err := ResolveLink(s.document.Path, target)
	if err != nil {
		edit.errorText = err.Error()
		return
	}
	if target != edit.info.Destination && resolved.Path != "" && (filepath.IsAbs(target) || strings.HasPrefix(strings.ToLower(target), "file:") || len(target) > 2 && target[1] == ':') {
		if s.options.OnImportResources == nil {
			edit.errorText = "当前文档不支持导入本地图片"
			return
		}
		s.importReplacementFiles(edit, []string{resolved.Path}, true)
		return
	}
	if resolved.URL != "" {
		parsed, _ := url.Parse(resolved.URL)
		if parsed.Hostname() == "" {
			edit.errorText = "图片网址缺少主机地址"
			return
		}
		if parsed.User != nil {
			edit.errorText = "图片网址不能包含账号或密码"
			return
		}
		if !strings.HasPrefix(strings.ToLower(resolved.URL), "http://") && !strings.HasPrefix(strings.ToLower(resolved.URL), "https://") {
			edit.errorText = "图片地址仅支持 HTTP(S) 或授权的本地图片"
			return
		}
	}
	if s.options.Native != nil && s.options.Native.ValidateImage != nil {
		if err := s.options.Native.ValidateImage(target); err != nil {
			edit.errorText = err.Error()
			return
		}
	}
	if err := s.native.ReplaceImage(edit.info, target, edit.alt); err != nil {
		edit.errorText = err.Error()
		return
	}
	s.cancelImageEdit()
	s.notify()
}

func (s *EditorState) importReplacement(edit *imageEdit) { s.importReplacementFiles(edit, nil, false) }

func (s *EditorState) importReplacementFiles(edit *imageEdit, files []string, save bool) {
	if !s.imageDialogCurrent(edit) || s.options.OnImportResources == nil {
		edit.errorText = ErrStale.Error()
		return
	}
	edit.importing = true
	edit.errorText = ""
	request := ImportRequest{DocumentID: s.document.ID, DocumentPath: s.document.Path, Version: edit.info.Version, Anchor: edit.info.Start, Caret: edit.info.End, Text: edit.alt, Pick: len(files) == 0, Files: files}
	ctx, cancel := context.WithCancel(s.reader.Context())
	edit.cancel = cancel
	s.options.OnImportResources(ctx, request, func(result ImportResult, err error) {
		cancel()
		edit.importing = false
		if !s.imageDialogCurrent(edit) {
			edit.errorText = ErrStale.Error()
			return
		}
		if err != nil {
			edit.errorText = err.Error()
			return
		}
		if len(result.Resources) == 0 {
			return
		}
		if len(result.Resources) != 1 || !result.Resources[0].Image {
			edit.errorText = "请选择一张图片"
			return
		}
		edit.target = result.Resources[0].Target
		if save {
			s.applyImageEdit(edit)
		}
	})
}

func (s *EditorState) imagePopover(c *ui.Context, surface ui.Element) {
	edit := s.image
	if edit == nil {
		return
	}
	if !edit.open {
		if pendingImageInput(edit) {
			edit.open = true
		} else {
			s.image = nil
			return
		}
	}
	position := edit.position
	if position.W <= 0 {
		position = surface.Bounds()
		position.X += 16
		position.Y += 16
	}
	width, height := c.Size()
	ui.Overlay(c, func() {
		// Keep the real editor as the focus-scope owner when this editor is
		// inside a form/comment modal; a free window anchor loses that scope.
		bounds := surface.Bounds()
		anchor := ui.Box(c.Key("image-edit-anchor")).AttachTo(surface, ui.AnchorTopStart, ui.AnchorTopStart).Left(max(6, min(position.X, width-366)) - bounds.X).Top(max(6, min(position.Y, height-245)) - bounds.Y).Width(1).Height(1)
		ui.PopoverBase(c, anchor, &edit.open, func(panel ui.Element) {
			panel.Width(min(float32(350), max(1, width-12))).Padding(12).Radius(8).Background(c.Theme().Background).Border(1, c.Theme().Border)
			if panel.OverlayShortcut(0, ui.KeyEscape) {
				s.cancelImageEdit()
				return
			}
			ui.Column(c.Key("image-editor")).FillWidth().Gap(8).Children(func() {
				ui.Text(c, "编辑图片").FontWeight(600)
				address := ui.TextInput(c.Key("image-address"), &edit.target).Label("图片地址").Placeholder("图片网址或相对路径").FillWidth().Height(34).Disabled(edit.importing)
				if edit.focus {
					address.Focus()
					edit.focus = false
				}
				ui.TextInput(c.Key("image-alt"), &edit.alt).Label("替代文字").Placeholder("替代文字（可选）").FillWidth().Height(34).Disabled(edit.importing)
				if s.options.OnImportResources != nil {
					if ui.Button(c, "选择本地图片").Disabled(edit.importing).Clicked() {
						s.importReplacement(edit)
					}
				}
				if edit.importing {
					ui.Text(c, "正在导入…").FontSize(12)
				}
				if edit.errorText != "" {
					ui.Text(c, edit.errorText).TextColor(c.Theme().Danger).FontSize(12).Wrap()
				}
				ui.Row(c).Gap(6).Children(func() {
					ui.Spacer(c)
					if ui.Button(c, "取消").Clicked() {
						s.cancelImageEdit()
					}
					if ui.Button(c, "保存").Disabled(edit.importing).Clicked() {
						s.applyImageEdit(edit)
					}
				})
			})
		})
	})
}
