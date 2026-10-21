package desktop

import (
	"fmt"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

func productionPhase(phase string) string {
	switch phase {
	case "INITIALIZING", "SYNC_MAIN":
		return "同步主分支"
	case "MAIN_READY":
		return "等待创建 release"
	case "CREATE_RELEASE":
		return "创建 release"
	case "MERGE_FEATURE":
		return "合并分支"
	case "SYNC_RELEASE":
		return "同步 release"
	case "CONFLICT":
		return "等待解决冲突"
	case "PAUSED":
		return "已暂停"
	case "TAG_READY":
		return "等待构建确认"
	case "PUSH_TAG":
		return "推送 Tag"
	case "ENDED":
		return "已结束"
	default:
		return "进行中"
	}
}

func productionStage(stage domain.GenbuStageStatus) string {
	switch stage {
	case domain.GenbuSuccess:
		return "成功"
	case domain.GenbuFailed:
		return "失败"
	case domain.GenbuBuilding:
		return "构建中"
	case domain.GenbuInitial:
		return "等待构建"
	default:
		return "待查询"
	}
}

func shortProductionSHA(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	return sha
}

func productionButton(c *ui.Context, key, text string, p palette, primary, disabled bool) bool {
	b := ui.ButtonBase(c.Key(key)).AlignSelf(ui.Start).Height(34).Padding(0, 14).Radius(20).Center().Disabled(disabled).Label(text)
	bg, fg := p.surface, p.text
	if primary {
		bg, fg = ui.Hex("#356AE6"), ui.Hex("#FFFFFF")
	} else {
		b.Border(1, p.line)
	}
	if disabled {
		bg, fg = p.soft, p.muted
	} else if b.Pressed() {
		if primary {
			bg = ui.Hex("#1F4AB5")
		} else {
			bg = p.selected
		}
	} else if b.Hovered() {
		if primary {
			bg = ui.Hex("#2454C6")
		} else {
			bg = p.soft
		}
	}
	if b.Focused() {
		b.Border(2, p.blue)
	}
	b.Background(bg).Children(func() { label(c, text, 13, fg).NoWrap() })
	return b.Clicked()
}

func (s *app) renderProductionPage(c *ui.Context, p palette) {
	state := s.production
	if state == nil {
		label(c, "正在载入生产发布…", 14, p.muted).Padding(24)
		return
	}
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(20).Gap(16).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
			label(c, "生产发布", 22, p.text).FontWeight(650).Grow(1)
		})
		ui.Row(c).Grow(1).MinHeight(0).Gap(20).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(220).Shrink(0).MinHeight(0).Gap(10).Children(func() {
				ui.TextInput(c.Key("production-service-query"), s.input("production-service-query", "")).FillWidth().Height(36).Placeholder("搜索服务").Label("搜索生产发布服务")
				query := strings.ToLower(strings.TrimSpace(*s.input("production-service-query", "")))
				grouped := productionHasMultipleGroups(state.choices)
				ui.Scroll(c).Grow(1).MinHeight(0).Gap(4).Children(func() {
					for _, service := range state.choices {
						if !service.matches(query) {
							continue
						}
						selection := production.Selection{GroupID: service.GroupID, ServiceID: service.ID}
						key := productionKey(selection)
						name := productionChoiceLabel(service, grouped)
						identity := service.GroupName + " · " + service.Name + "\n" + service.RepositoryName + "\n" + service.MasterBranch + "\n" + service.RepositoryPath
						b := ui.ButtonBase(c.Key("production-service-"+key)).Row().AlignItems(ui.Center).FillWidth().Height(44).Padding(0, 12).Radius(12).Label(name).Tooltip(identity)
						if state.selected == key {
							b.Background(p.selected)
						} else if b.Hovered() {
							b.Background(p.soft)
						}
						b.Children(func() {
							label(c, name, 13, p.text).FontWeight(550).Grow(1).MinWidth(0).SingleLine()
						})
						if b.Clicked() {
							state.selected = key
							if !service.Historical && state.views[key].Preview == nil && !state.views[key].Busy {
								s.productionInspect("")
							}
						}
					}
				})
			})
			ui.Box(c).Width(1).Background(p.line)
			ui.Scroll(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				ui.Column(c.Key("production-content")).FillWidth().MaxWidth(1080).AlignSelf(ui.Start).MinWidth(0).Gap(16).Label("生产发布操作区").Children(func() { s.renderProductionDetail(c, p) })
			})
		})
	})
}

func (s *app) renderProductionDetail(c *ui.Context, p palette) {
	v := s.productionView()
	if v == nil {
		label(c, "先在服务页添加并启用一个仓库服务。", 14, p.muted).Wrap()
		return
	}
	b := s.productionBatch(v)
	for _, choice := range s.production.choices {
		if choice.GroupID == v.Selection.GroupID && choice.ID == v.Selection.ServiceID && choice.Historical {
			label(c, "原服务已停用或移除。可查看历史、保留工作区或结束批次；继续发布前请恢复并核对服务配置。", 12, p.amber).Wrap()
			break
		}
	}
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		name := "选择服务后检查生产基线"
		for _, choice := range s.production.choices {
			if choice.ID == v.Selection.ServiceID && choice.GroupID == v.Selection.GroupID {
				name = choice.Name
				break
			}
		}
		label(c, name, 18, p.text).FontWeight(600).MinWidth(0).SingleLine().Tooltip(name)
		if b != nil {
			label(c, firstNonEmpty(b.ReleaseBranch, "等待创建 release"), 12, p.muted).MinWidth(0).SingleLine().Tooltip(b.ReleaseBranch)
			compactState(c, productionPhase(b.Phase), p)
		}
		ui.Box(c).Grow(1)
		miniButton(c, p, "production-more", "more", "更多生产发布操作", false).Disabled(v.Busy).Menu(func(menu *ui.Menu) {
			if menu.Item("手动确认生产 Tag").Disabled(v.Busy).Chosen() {
				s.openProductionBaseline(v)
			}
			if b != nil && !b.Ended {
				menu.Separator()
				if menu.Item("结束批次").Disabled(v.Busy).Chosen() {
					s.confirmProductionEnd(v, *b)
				}
			}
			if b != nil && b.Ended {
				menu.Separator()
				if menu.Item("删除批次").Icon(icons["delete"]).Dangerous().Disabled(v.Busy).Chosen() {
					s.confirmProductionDelete(v, *b)
				}
			}
		})
	})
	if v.ActiveStep == 0 {
		s.renderProductionSectionFeedback(c, p, s.productionSection(v, b, 0))
	}
	if b == nil || !b.Ended {
		s.renderProductionInitialization(c, p, v, b)
	}
	if b == nil || b.Ended {
		if b != nil {
			label(c, "正在查看已结束批次："+b.ReleaseBranch, 13, p.muted).Wrap()
			s.renderProductionCheckActions(c, p, v)
			s.renderProductionSectionFeedback(c, p, s.productionSection(v, b, 1))
			s.renderProductionPendingHistory(c, p, v, *b)
			s.renderProductionBatchHistory(c, p, *b)
			s.renderProductionSectionFeedback(c, p, s.productionSection(v, b, 6))
		}
		if b != nil && productionButton(c, "production-new", "新建批次", p, true, v.Busy) {
			v.selectProductionBatch("")
			v.Version = ""
			s.productionInspect("")
		}
	} else {
		s.renderProductionActive(c, p, v, *b)
	}
	if b != nil && len(b.RetainedWorktrees) > 0 {
		label(c, "保留的工作区", 13, p.text).FontWeight(600)
		for _, path := range b.RetainedWorktrees {
			label(c, path, 11, p.muted).Wrap().Selectable()
		}
	}
	s.renderProductionHistory(c, p, v)
}

func (s *app) renderProductionBaseline(c *ui.Context, p palette, v *productionView) {
	preview := v.Preview
	ui.Column(c).FillWidth().Gap(7).Padding(12, 14).Background(p.soft).Radius(12).Children(func() {
		ui.Row(c).FillWidth().Gap(10).Children(func() {
			label(c, "线上生产 Tag", 12, p.muted)
			label(c, firstNonEmpty(preview.ProductionTag, "待确认"), 13, p.text).Grow(1).SingleLine()
		})
		label(c, "主分支  "+preview.Target.Remote+"/"+preview.Target.MainBranch+" · "+shortProductionSHA(preview.MainSHA), 12, p.muted).Wrap()
		if len(preview.ProductionCandidates) > 1 {
			label(c, "线上存在多个版本："+strings.Join(preview.ProductionCandidates, "、"), 12, p.amber).Wrap()
		}
		if preview.Warning != "" {
			label(c, preview.Warning, 12, p.amber).Wrap()
		}
	})
	if preview.Manual {
		label(c, "当前基线由手动指定，可在“更多”中恢复自动识别。", 11, p.amber).Wrap()
	}
}

func (s *app) renderProductionActive(c *ui.Context, p palette, v *productionView, b production.Batch) {
	if b.InitialSHA == "" {
		return
	}
	pending := productionIntentStep(&b)
	blocked := v.Busy || b.Intent != nil || b.InitialSHA == ""
	m := s.productionReleaseModel(v, &b)
	productionZone(c, p, "合并分支", func() {
		s.renderProductionFeaturePicker(c, p, v, b, blocked)
		s.renderProductionSectionFeedback(c, p, s.productionSection(v, &b, 4))
		if pending == 4 {
			s.renderProductionRecovery(c, p, v, b)
		}
		if b.Intent == nil && v.Preview != nil && v.Preview.ReleaseSHA != "" && v.Preview.ReleaseSHA != b.ReleaseSHA {
			label(c, "远端 release 有批次之外的新提交，请核对差异。", 12, p.amber).Wrap()
			if productionButton(c, "production-adopt", "查看并采纳远端", p, false, v.Busy) {
				s.confirmProductionAdopt(v, b)
			}
		}
		if len(b.Branches) > 0 {
			productionDisclosure(c, p, "production-merged-history", fmt.Sprintf("合并记录 · %d 次", len(b.Branches)), &v.BranchHistoryExpanded, func() {
				ui.Scroll(c.Key("production-feature-history-" + b.ID)).FillWidth().MaxHeight(110).Gap(4).Children(func() {
					for i := len(b.Branches) - 1; i >= 0; i-- {
						branch := b.Branches[i]
						label(c, branch.Branch+" · "+shortProductionSHA(branch.SourceSHA), 11, p.muted).SingleLine().Tooltip(branch.Branch + "\n" + branch.SourceSHA)
					}
				})
			})
		}
	})
	productionZone(c, p, "构建生产 Tag", func() {
		if m.Ready {
			label(c, b.TagPlan.Tag+" · "+shortProductionSHA(b.TagPlan.Commit), 13, p.blue).FontWeight(550).Wrap()
		} else {
			label(c, m.BuildReason, 12, p.muted).Wrap()
		}
		ui.Row(c).FillWidth().Gap(8).Wrap().Children(func() {
			if productionButton(c, "production-publish", "构建 Tag", p, true, blocked || !m.CanBuild) {
				s.productionInspect("build-preview")
			}
			if m.Ready && productionButton(c, "production-tag-diff", "查看本次构建差异", p, false, false) {
				s.openProductionDiff(v, "本次构建差异", b.ReleaseBranch, firstNonEmpty(lastProductionTagName(b), "release 初始提交"), b.TagPlan.Commit, b.LastTagCommit(), false)
			}
		})
		s.renderProductionSectionFeedback(c, p, m.Build)
		if m.ShowSync {
			s.renderProductionRelations(c, p, v, b, blocked)
		}
		if pending == 5 || pending == 6 {
			s.renderProductionRecovery(c, p, v, b)
		}
		s.renderProductionRecentTag(c, p, v, b)
	})
}

func (s *app) renderProductionFeaturePicker(c *ui.Context, p palette, v *productionView, b production.Batch, disabled bool) {
	label(c, "选择要合并的远端分支", 12, p.text)
	label(c, "合入到："+b.ReleaseBranch, 12, p.muted).Wrap().Tooltip(b.Target.Remote + "/" + b.ReleaseBranch)
	ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
		previous := v.Branch
		combo := ui.ComboboxBase(c.Key("production-feature"), &v.Branch)
		combo.Input.Width(560).MaxWidthPercent(100).MinWidth(0).Height(38).Padding(0, 34, 0, 12).Radius(8).Background(p.surface).Border(1, p.line).Placeholder("搜索或填写远端分支").ReadOnly(disabled).Disabled(disabled).Label("待合并的单个远端分支").Tooltip(v.Branch).OnChange(v.productionBranchChanged)
		if disabled {
			combo.Input.Background(p.soft)
		} else if combo.Input.Focused() {
			combo.Input.Border(2, p.blue)
		} else if combo.Input.Hovered() {
			combo.Input.Border(1, p.blue)
		}
		drawTaskComboboxChevron(combo.Input, p)
		combo.Popup(func(panel ui.Element) {
			if disabled {
				return
			}
			panel.Padding(4).MaxHeight(260).Background(p.surface).Border(1, p.line).Radius(9)
			ui.Scroll(c).FillWidth().MaxHeight(252).Children(func() {
				if v.Preview == nil {
					return
				}
				for _, branch := range v.Preview.Branches {
					if branch == b.ReleaseBranch || branch == b.Target.MainBranch {
						continue
					}
					if combo.Filtering() && !strings.Contains(strings.ToLower(branch), strings.ToLower(v.Branch)) {
						continue
					}
					item := combo.Item(branch).FillWidth().Height(30).Padding(0, 10).Radius(6)
					if item.Highlighted() {
						item.Background(p.selected)
					}
					item.Tooltip(branch).Children(func() { label(c, branch, 12, p.text).SingleLine() })
				}
			})
		})
		if branch, chosen := combo.Chosen(); chosen && !disabled {
			v.Branch = branch
		}
		if previous != v.Branch {
			v.productionBranchChanged()
		}
		ui.Row(c).Gap(8).Children(func() {
			if productionButton(c, "production-feature-diff", "查看差异", p, false, disabled || strings.TrimSpace(v.Branch) == "") {
				s.productionRequestFor(v, application.Action{Kind: "production.feature-preview", Payload: map[string]any{"batchId": b.ID, "branch": v.Branch}}, "feature-diff")
			}
			if productionButton(c, "production-merge", "合并到 release", p, true, disabled || strings.TrimSpace(v.Branch) == "") {
				s.productionRequestFor(v, application.Action{Kind: "production.feature-preview", Payload: map[string]any{"batchId": b.ID, "branch": v.Branch}}, "feature-preview")
			}
		})
	})
}

func (s *app) renderProductionRecovery(c *ui.Context, p palette, v *productionView, b production.Batch) {
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		if merge := b.Intent.Merge; merge != nil {
			label(c, "目标 "+b.Target.Remote+"/"+merge.TargetBranch+" · 源提交 "+shortProductionSHA(merge.SourceSHA), 12, p.muted).Wrap()
			if len(merge.Conflicts) > 0 {
				ui.Scroll(c).FillWidth().MaxHeight(64).Children(func() {
					label(c, strings.Join(merge.Conflicts, "\n"), 12, p.danger).Wrap().Selectable()
				})
			}
			label(c, merge.Worktree, 11, p.muted).Wrap().Selectable()
		}
		ui.Row(c).FillWidth().Gap(8).Wrap().Children(func() {
			if b.Intent.Merge != nil && productionButton(c, "production-open-ide", "打开 IDE", p, false, v.Busy) {
				s.productionRequestFor(v, application.Action{Kind: "production.open-ide", Payload: map[string]any{"batchId": b.ID}}, "")
			}
			if productionButton(c, "production-resume", "校验并继续", p, true, v.Busy) {
				s.confirmProductionResume(v, b)
			}
			if b.Intent.Merge != nil && productionButton(c, "production-pending-preview", "核对远端变化", p, false, v.Busy) {
				s.productionRequestFor(v, application.Action{Kind: "production.pending-preview", Payload: map[string]any{"batchId": b.ID}}, "pending-preview")
			}
			if b.Intent.Kind == "tag" && productionButton(c, "production-reprepare-tag", "重新检查编号与基线", p, false, v.Busy) {
				s.confirmProductionReprepare(v, b)
			}
			if b.Intent.Step == "main" && v.Preview != nil && productionButton(c, "production-reconfirm-main", "重新确认生产基线并合并主分支", p, false, v.Busy) {
				s.confirmProductionMain(v)
			}
		})
	})
}

func (s *app) renderProductionPendingHistory(c *ui.Context, p palette, v *productionView, b production.Batch) {
	i := b.Intent
	if i == nil {
		return
	}
	label(c, "保留的操作记录 · "+i.Step, 13, p.text).FontWeight(600)
	label(c, "批次已结束，仅核对远端结果，不再执行合并或推送。", 12, p.muted).Wrap()
	if i.Kind == "tag" {
		label(c, "未确认 Tag："+i.Tag, 13, p.amber)
		label(c, "提交："+i.TagCommit+"\n对象："+i.TagObject, 11, p.muted).Wrap().Selectable()
	}
	if m := i.Merge; m != nil {
		label(c, "目标："+b.Target.Remote+"/"+m.TargetBranch+"\n源提交："+m.SourceSHA+"\n原目标："+m.BeforeSHA, 11, p.muted).Wrap().Selectable()
		label(c, m.Worktree, 11, p.muted).Wrap().Selectable()
		if len(m.Conflicts) > 0 {
			label(c, strings.Join(m.Conflicts, "\n"), 11, p.amber).Wrap()
		}
		if productionButton(c, "production-ended-open-ide", "打开保留工作区", p, false, v.Busy) {
			s.productionRequestFor(v, application.Action{Kind: "production.open-ide", Payload: map[string]any{"batchId": b.ID}}, "")
		}
	}
	if i.ResultConfirmed {
		label(c, "原步骤已确认；后续阶段未继续。", 12, p.muted).Wrap()
	} else if productionButton(c, "production-ended-reconcile", "核对远端结果", p, false, v.Busy) {
		s.productionRequestFor(v, application.Action{Kind: "production.reconcile-ended", Payload: map[string]any{"batchId": b.ID}}, "")
	}
}

func (s *app) renderProductionBatchHistory(c *ui.Context, p palette, b production.Batch) {
	if len(b.Tags) == 0 {
		return
	}
	v := s.productionView()
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		label(c, "生产 Tag", 14, p.text).FontWeight(600).Grow(1)
		if productionButton(c, "production-refresh-build", "刷新构建状态", p, false, v == nil || v.Busy) {
			s.productionRequestFor(v, application.Action{Kind: "production.refresh-build", Payload: map[string]any{"batchId": b.ID}}, "")
		}
	})
	for i := len(b.Tags) - 1; i >= 0; i-- {
		tag := b.Tags[i]
		ui.Column(c).FillWidth().Gap(5).Padding(10, 0).Children(func() {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				label(c, tag.Name, 14, p.blue).FontWeight(600)
				label(c, shortProductionSHA(tag.Commit), 11, p.muted).Grow(1).SingleLine()
				compactState(c, "构建 "+productionStage(tag.Status.Build), p)
			})
			if tag.Status.FailureReason != nil {
				label(c, *tag.Status.FailureReason, 11, p.amber).Wrap()
			}
			if tag.Status.Build == domain.GenbuFailed {
				label(c, "请到平台重新编译此 Tag；没有新增提交时不会生成新 Tag。", 11, p.muted).Wrap()
			}
		})
	}
}

func productionTargetDetail(t production.Target) string {
	return fmt.Sprintf("服务：%s\n项目组：%s\n仓库标识：%s\n仓库路径：%s\n远端：%s/%s", t.ServiceName, t.GroupID, t.RepositoryID, t.Repository, t.Remote, t.MainBranch)
}

func (s *app) confirmProductionCreate(v *productionView) {
	if v.Preview == nil || (v.Preview.Manual && v.ManualTag != v.Preview.ProductionTag) || (!v.Preview.Manual && v.ManualTag != "") {
		v.Error = "生产基线输入已变化，请重新检查。"
		return
	}
	p, id := *v.Preview, v.BatchID
	version, err := production.ReleaseVersion(v.Version, p.Tags)
	if err != nil {
		v.Error = err.Error()
		return
	}
	detail := productionTargetDetail(p.Target) + fmt.Sprintf("\n生产 Tag：%s (%s)\n主分支提交：%s\n新分支：release/%s\n主分支已有提交会全部保留。\n\n%s\n%s", p.ProductionTag, p.ProductionSHA, p.MainSHA, version, p.Commits, p.Diff)
	s.confirmProduction(v, p.Target, productionConfirmationContent{
		Title: "创建 release 分支", Message: "确认创建并推送新分支？", Detail: detail,
		Source: productionCommitLabel(p.Target.Remote+"/"+p.Target.MainBranch, p.MainSHA), Destination: p.Target.Remote + "/release/" + version,
		Notices: []string{"本次仅创建并推送新分支。"},
	}, func() {
		s.productionRequestFor(v, application.Action{Kind: "production.create", Payload: map[string]any{"batchId": id, "preview": p, "version": version, "confirmed": true}}, "")
	})
}

func (s *app) confirmProductionFeature(v *productionView, p production.Preview, branch string) {
	b := s.productionBatch(v)
	if b == nil {
		return
	}
	id, source, target := b.ID, p.BranchHeads[branch], p.ReleaseSHA
	detail := productionTargetDetail(b.Target) + fmt.Sprintf("\n%s/%s → %s\n源提交：%s\n目标提交：%s\n\n%s\n%s", b.Target.Remote, branch, b.ReleaseBranch, source, target, p.Commits, p.Diff)
	s.confirmProduction(v, b.Target, productionConfirmationContent{
		Title: "合并功能分支", Message: "确认合并功能分支？", Detail: detail,
		Source: productionCommitLabel(b.Target.Remote+"/"+branch, source), Destination: productionCommitLabel(b.Target.Remote+"/"+b.ReleaseBranch, target),
		Notices: []string{"保留 merge commit，合并成功后推送 release。"},
	}, func() {
		s.productionRequestFor(v, application.Action{Kind: "production.merge", Payload: map[string]any{"batchId": id, "branch": branch, "sourceSha": source, "releaseSha": target, "confirmed": true}}, "")
	})
}

func (s *app) confirmProductionSync(v *productionView, p production.Preview) {
	b := s.productionBatch(v)
	if b == nil || b.Ended || p.ProductionSHA == "" {
		return
	}
	id := b.ID
	detail := productionTargetDetail(b.Target) + fmt.Sprintf("\n生产 Tag：%s (%s)\n主分支提交：%s\nrelease：%s (%s)\n\n%s\n%s", p.ProductionTag, p.ProductionSHA, p.MainSHA, b.ReleaseBranch, p.ReleaseSHA, p.Commits, p.Diff)
	notices := []string{"合并成功后推送当前 release。"}
	if b.Intent != nil {
		detail += "\n未完成的旧工作区将保留；使用本次确认的基线重新同步，不复用旧合并结果。"
		notices = append(notices, "未完成的旧工作区将保留；使用本次确认的基线重新同步。")
	}
	s.confirmProduction(v, b.Target, productionConfirmationContent{
		Title: "合并主分支到 release", Message: "确认合并主分支？", Detail: detail, Notices: notices,
		Source: productionCommitLabel(b.Target.Remote+"/"+b.Target.MainBranch, p.MainSHA), Destination: productionCommitLabel(b.Target.Remote+"/"+b.ReleaseBranch, p.ReleaseSHA),
	}, func() {
		s.productionRequestFor(v, application.Action{Kind: "production.merge-release", Payload: map[string]any{"batchId": id, "preview": p, "confirmed": true}}, "")
	})
}

func (s *app) confirmProductionTag(v *productionView, b production.Batch) {
	if !productionTagPlanCurrent(v, &b) {
		return
	}
	plan := *b.TagPlan
	detail := productionTargetDetail(b.Target) + fmt.Sprintf("\nrelease：%s\n最终提交：%s\n生产 Tag：%s\n\n%s\n%s", b.ReleaseBranch, plan.Commit, plan.Tag, plan.Commits, plan.Diff)
	s.confirmProduction(v, b.Target, productionConfirmationContent{
		Title: "构建生产 Tag", Message: "确认创建并推送生产 Tag？", Detail: detail,
		Source: productionCommitLabel(b.Target.Remote+"/"+b.ReleaseBranch, plan.Commit), Destination: "Tag " + plan.Tag,
		Notices: []string{"同名 Tag 不覆盖；推送后跟踪构建结果。"},
	}, func() {
		s.productionRequestFor(v, application.Action{Kind: "production.publish", Payload: map[string]any{"batchId": b.ID, "token": plan.Token, "confirmed": true}}, "")
	})
}

func (s *app) confirmProductionPending(v *productionView, b production.Batch, p production.Preview) {
	if b.Intent == nil || b.Intent.Merge == nil {
		return
	}
	id, sha := b.ID, p.MainSHA
	detail := productionTargetDetail(b.Target) + fmt.Sprintf("\n目标：%s/%s\n原目标：%s\n当前远端：%s\n原工作区会保留。\n\n%s\n%s", b.Target.Remote, b.Intent.Merge.TargetBranch, b.Intent.Merge.BeforeSHA, sha, p.Commits, p.Diff)
	s.confirmProduction(v, b.Target, productionConfirmationContent{
		Title: "核对远端并继续", Message: "确认采纳远端新增提交，并继续已确认的合并？", Detail: detail,
		Source: productionCommitLabel(b.Target.Remote+"/"+b.Intent.Merge.TargetBranch, sha), Destination: productionCommitLabel("当前合并 · "+b.Intent.Merge.TargetBranch, b.Intent.Merge.BeforeSHA),
		Notices: []string{"保留原工作区，仅继续已确认的合并。"},
	}, func() {
		s.productionRequestFor(v, application.Action{Kind: "production.rebase-pending", Payload: map[string]any{"batchId": id, "remoteSha": sha, "confirmed": true}}, "")
	})
}

func lastProductionTagName(b production.Batch) string {
	if len(b.Tags) == 0 {
		return ""
	}
	return b.Tags[len(b.Tags)-1].Name
}

func (s *app) confirmProductionMain(v *productionView) {
	if v == nil || v.Preview == nil || v.Preview.ProductionSHA == "" {
		return
	}
	p, id := *v.Preview, v.BatchID
	detail := productionTargetDetail(p.Target) + fmt.Sprintf("\n生产 Tag：%s (%s)\n目标：%s/%s (%s)\n主分支已有提交全部保留。\n\n%s\n%s", p.ProductionTag, p.ProductionSHA, p.Target.Remote, p.Target.MainBranch, p.MainSHA, p.Commits, p.Diff)
	notices := []string{"保留主分支已有全部代码；合并成功后推送。"}
	if b := s.productionBatch(v); b != nil && b.Intent != nil {
		detail += "\n重新确认后保留原合并工作区，只执行本次生产 Tag 到主分支的合并。"
		notices = append(notices, "保留原合并工作区，只执行本次生产 Tag 到主分支的合并。")
	}
	s.confirmProduction(v, p.Target, productionConfirmationContent{
		Title: "合并生产 Tag 到 " + p.Target.MainBranch, Message: "确认合并并推送主分支？", Detail: detail, Notices: notices,
		Source: productionCommitLabel("Tag "+p.ProductionTag, p.ProductionSHA), Destination: productionCommitLabel(p.Target.Remote+"/"+p.Target.MainBranch, p.MainSHA),
	}, func() {
		s.productionRequestFor(v, application.Action{Kind: "production.merge-main", Payload: map[string]any{"batchId": id, "preview": p, "confirmed": true}}, "")
	})
}
