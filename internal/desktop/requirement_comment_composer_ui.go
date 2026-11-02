package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func (s *app) renderRequirementCommentComposer(c *ui.Context, p palette) {
	if s.requirementUnit.state.commentComposers == nil {
		return
	}
	state := s.requirementUnit.state.commentComposers.active
	if !s.commentComposerCurrent(state) || !state.open {
		return
	}
	locked := !state.loaded || state.draft.Pending() || state.running && state.operation != "save" && state.operation != "import-images"
	blocked := locked || state.running || state.dirty || commentImportHasResources(state) || s.confirmationDialog != nil
	editing := state.editCommentID != ""
	title, primary, discard := "添加评论", "发布评论", "清空草稿"
	dialogLabel := "添加需求评论弹窗"
	if editing {
		title, primary, discard = "编辑评论", "保存修改", "放弃修改"
		dialogLabel = "编辑需求评论弹窗"
	}
	dialog := ui.DialogBase(c.Key("requirement-comment-composer"), &state.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .38))
		w, h := c.Size()
		panel.Width(max(float32(1), min(float32(900), w-32))).Height(max(float32(1), min(float32(760), h-32))).Padding(20).Gap(14).Background(p.surface).Border(1, p.line).Radius(16).Label(dialogLabel)
		panel.Children(func() {
			ui.Column(c).FillWidth().MinWidth(0).Gap(5).Shrink(0).Children(func() {
				label(c, title, 18, p.text).FontWeight(600)
				name := state.draft.Item.Title
				if name == "" {
					name = state.url
				}
				label(c, name, 13, p.text).FillWidth().Wrap().Selectable()
			})
			ui.Row(c).FillWidth().Gap(8).Wrap().Shrink(0).Children(func() {
				ui.Spacer(c).Grow(1)
				if requirementProgressButton(c, p, "comment-add-images", "添加图片", blocked).Clicked() {
					s.chooseCommentFiles(state, true)
				}
				if requirementProgressButton(c, p, "comment-paste-image", "粘贴截图", blocked).Clicked() {
					s.pasteCommentScreenshot(state)
				}
				if requirementProgressButton(c, p, "comment-add-attachment", "添加附件", blocked || state.draft.Attachment != nil).Clicked() {
					s.chooseCommentFiles(state, false)
				}
			})
			ui.Column(c).FillWidth().Grow(1).MinHeight(0).MinWidth(0).Gap(10).Children(func() {
				s.renderCommentMarkdownEditor(c, p, state, locked)
				if images := commentLegacyPendingImages(state.draft); len(images) > 0 {
					ui.Scroll(c.Key("comment-legacy-pending-images")).FillWidth().MinHeight(0).MaxHeight(120).Children(func() {
						for _, image := range images {
							asset := image
							ui.Row(c.Key("legacy-image-" + asset.ID)).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
								label(c, "待核对图片 · "+asset.Name, 12, p.muted).Grow(1).MinWidth(0).Wrap()
								ui.Button(c, "查看图片").Label("待核对图片：" + asset.Name).Disabled(asset.LocalPath == "").OnClick(func() {
									if s.commentComposerCurrent(state) && state.draft.Pending() {
										s.openMarkdownLocalImageViewer(state.markdown, asset.LocalPath)
									}
								})
							})
						}
					})
				}
				if state.draft.Attachment != nil {
					ui.Scroll(c.Key("comment-staged-files")).FillWidth().MinHeight(0).MaxHeight(145).Gap(6).Label("评论图片及附件").Children(func() {
						if a := state.draft.Attachment; a != nil {
							s.renderCommentDraftAsset(c, p, state, *a, blocked)
						}
					})
				}
			})
			ui.Column(c).FillWidth().MinWidth(0).Gap(6).Shrink(0).Children(func() {
				if state.running && state.operation != "save" {
					stage := state.progress.Stage
					if stage == "" {
						stage = map[string]string{"load": "读取草稿", "publish": "准备发布", "check": "核对发布结果"}[state.operation]
						if editing {
							stage = map[string]string{"load": "读取原评论", "reload": "读取原评论", "publish": "准备保存", "check": "核对修改结果"}[state.operation]
						}
						if stage == "" {
							stage = "准备文件"
						}
					}
					if state.progress.Total > 0 {
						stage += fmt.Sprintf(" · 已上传 %d／%d", state.progress.Completed, state.progress.Total)
					}
					label(c, stage, 12, p.blue).FillWidth().Wrap()
				}
				if state.draft.Pending() {
					message := "发布结果待核对，请先核对原需求中的评论。"
					if editing {
						message = "修改结果待核对，请先核对原评论；不会自动重新保存。"
					}
					label(c, message, 12, p.amber).FillWidth().Wrap()
				} else if editing && state.draft.Edit != nil && state.draft.Edit.Conflict {
					label(c, "原评论已在远端修改，本地修改已保留。", 12, p.amber).FillWidth().Wrap()
				}
				if state.errorText != "" {
					ui.Scroll(c.Key("comment-error-details")).FillWidth().MinHeight(0).MaxHeight(90).Children(func() { label(c, state.errorText, 12, p.danger).FillWidth().Wrap().Selectable() })
				}
				ui.Row(c).FillWidth().Gap(8).Wrap().Justify(ui.End).Children(func() {
					if state.draft.Pending() {
						checkLabel := "核对发布结果"
						if editing {
							checkLabel = "核对修改结果"
						}
						if requirementProgressButton(c, p, "comment-check-result", checkLabel, state.running).Clicked() {
							s.startCommentComposerJob(state, "check", nil)
						}
						if requirementProgressButton(c, p, "comment-open-original", "打开原需求", false).Clicked() {
							s.dispatchIndependentSystemOpen(application.Action{Kind: "system.open", Path: state.url})
						}
						if !editing && requirementProgressButton(c, p, "comment-allow-retry", "重新发布…", state.running).Clicked() {
							s.confirmCommentDraftChange(state, true)
						}
					} else {
						if requirementProgressButton(c, p, "comment-clear-draft", discard, blocked || !editing && !state.draft.HasContent()).Clicked() {
							s.confirmCommentDraftChange(state, false)
						}
						if editing && state.draft.Edit != nil && state.draft.Edit.Conflict && requirementProgressButton(c, p, "comment-reload-original", "重新载入原评论", blocked).Clicked() {
							s.confirmReloadCommentEdit(state)
						}
						if state.errorText != "" && requirementProgressButton(c, p, "comment-retry-local", "重试", state.running).Clicked() {
							op := "load"
							if state.dirty {
								op = "save"
							}
							s.startCommentComposerJob(state, op, nil)
						}
					}
					text := "关闭"
					if state.running && state.operation != "save" {
						text = "收起"
					}
					if requirementProgressButton(c, p, "comment-composer-hide", text, false).Clicked() {
						s.hideRequirementCommentComposer(state)
					}
					if !state.draft.Pending() {
						b := ui.ButtonBase(c.Key("comment-publish")).Height(34).Padding(0, 16).Center().Radius(20).Background(p.blue).Border(1, p.blue).Label(primary).Disabled(!commentComposerCanPublish(state))
						if b.Focused() {
							b.Border(2, p.text)
						}
						b.Children(func() {
							color := ui.Hex("#FFFFFF")
							if s.dark {
								color = p.bg
							}
							label(c, primary, 13, color).NoWrap()
						})
						if b.Clicked() {
							s.startCommentComposerJob(state, "publish", nil)
						}
					}
				})
			})
		})
	})
	if dialog.Dismissed() {
		s.hideRequirementCommentComposer(state)
	}
}

func (s *app) hideRequirementCommentComposer(state *requirementCommentComposer) {
	if !s.captureCommentMarkdownInput(state) {
		state.open = true
		return
	}
	state.open = false
	if state.dirty && !state.running && !state.draft.Pending() {
		s.startCommentComposerJob(state, "save", nil)
	}
}

func (s *app) renderCommentDraftAsset(c *ui.Context, p palette, state *requirementCommentComposer, a domain.CommentDraftAsset, blocked bool) {
	ui.Column(c.Key("comment-file-" + a.ID)).FillWidth().MinWidth(0).Gap(4).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
			label(c, fmt.Sprintf("附件 · %s · %s", a.Name, commentFileSize(a.Size)), 12, p.text).Grow(1).MinWidth(0).Wrap().Selectable()
			if requirementProgressButton(c, p, "comment-remove-"+a.ID, "移除", blocked).Clicked() {
				s.startCommentComposerJob(state, "remove", map[string]any{"assetID": a.ID})
			}
		})
		if a.Error != "" {
			label(c, a.Error, 12, p.danger).FillWidth().Wrap()
		}
	})
}

func commentFileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
}

// Reconciliation must retain the exact legacy body and upload credentials.
// Its append-only images remain available through explicit read-only controls.
func commentLegacyPendingImages(draft domain.RequirementCommentDraft) []domain.CommentDraftAsset {
	if !draft.Pending() {
		return nil
	}
	var images []domain.CommentDraftAsset
	for _, asset := range draft.Images {
		if asset.MarkdownRef == "" && (asset.OriginalURL == "" || !strings.Contains(draft.Content, asset.OriginalURL)) {
			images = append(images, asset)
		}
	}
	return images
}
