package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

// PendingTagSubmission is a presentation-only admission receipt. Its nonce and
// selection keys are never operation IDs, persisted history, or backend input.
type PendingTagSubmission struct {
	Nonce      string
	Scope      application.TagJobScope
	Title      string
	Items      []pendingTagItem
	knownItems map[string]bool
	focus      bool
}

type pendingTagItem struct {
	selectionKey, serviceName, moduleName, sourceBranch, targetBranch, remote, mode, path string
}

func (s *app) preparePendingTagSubmission(scope application.TagJobScope, action application.Action, view *tagDesktopJobView) *PendingTagSubmission {
	if action.Kind != "tag.build" {
		return nil
	}
	keys := tagJobSelectionKeys(action.Payload)
	if action.ServiceID != "" {
		keys = []string{action.ServiceID + ":" + action.ModuleID}
	}
	t := s.taskUnit.currentTask()
	if all, _ := action.Payload["allServices"].(bool); all && len(keys) == 0 {
		for _, workspace := range t.workspaces {
			if workspace.Record.TagEnabled {
				keys = append(keys, tagBuildWorkspaceKey(workspace))
			}
		}
	}
	if len(keys) == 0 {
		return nil
	}
	pending := &PendingTagSubmission{Scope: scope, Title: firstNonEmpty(t.title, "Tag 构建"), knownItems: map[string]bool{}, focus: true}
	for _, item := range view.snapshot.Items {
		pending.knownItems[item.ID] = true
	}
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		found := false
		for _, workspace := range t.workspaces {
			if tagBuildWorkspaceKey(workspace) != key {
				continue
			}
			target := ""
			if workspace.Record.TagTargetRef != nil {
				target = *workspace.Record.TagTargetRef
			}
			pending.Items = append(pending.Items, pendingTagItem{selectionKey: key, serviceName: firstNonEmpty(workspace.Record.ServiceName, workspace.Name, workspace.ServiceID), moduleName: firstNonEmpty(workspace.Record.ModuleName, workspace.Name, workspace.ModuleID), sourceBranch: workspace.Record.Branch, targetBranch: target, remote: workspace.Record.PushRemote, mode: string(workspace.Record.TagMode), path: workspace.Path})
			found = true
			break
		}
		// Actual desktop controls pass canonical keys from the captured task.
		// An unknown/alias selection remains solely the backend's validation.
		if !found {
			return nil
		}
	}
	return pending
}

func samePendingTagItems(left, right *PendingTagSubmission) bool {
	if left.Scope != right.Scope || len(left.Items) != len(right.Items) {
		return false
	}
	byKey := make(map[string]pendingTagItem, len(left.Items))
	for _, item := range left.Items {
		byKey[item.selectionKey] = item
	}
	for _, item := range right.Items {
		if byKey[item.selectionKey] != item {
			return false
		}
	}
	return true
}

func hasPendingTagSubmission(view *tagDesktopJobView) bool {
	if view == nil {
		return false
	}
	for _, command := range view.commands {
		if command.pending != nil {
			return true
		}
	}
	return false
}

func visiblePendingTagItems(view *tagDesktopJobView, pending *PendingTagSubmission) []pendingTagItem {
	items := make([]pendingTagItem, 0, len(pending.Items))
	for _, candidate := range pending.Items {
		accepted := false
		for _, item := range view.snapshot.Items {
			if item.Kind == "tag.build" && item.SelectionKey == candidate.selectionKey && !pending.knownItems[item.ID] && item.Record != nil {
				accepted = true
				break
			}
		}
		if !accepted {
			items = append(items, candidate)
		}
	}
	return items
}

func (s *app) renderPendingTagSubmissions(c *ui.Context, p palette) bool {
	view := s.currentTagJobView()
	if view == nil || s.releaseUnit.state.tagJobs == nil {
		return false
	}
	s.releaseUnit.state.tagJobs.mu.Lock()
	closed := s.releaseUnit.state.tagJobs.closed
	s.releaseUnit.state.tagJobs.mu.Unlock()
	if closed {
		return false
	}
	visible := false
	for _, command := range view.commands {
		pending := command.pending
		if pending == nil || pending.Scope != s.tagJobScope() {
			continue
		}
		items := visiblePendingTagItems(view, pending)
		if len(items) == 0 {
			continue
		}
		visible = true
		card := ui.Column(c.Key(pending.Nonce)).FillWidth().Padding(8, 12).Gap(4).Background(p.surface).Border(1, p.line).Radius(12).Label("Tag 构建准备 · " + pending.Nonce)
		if pending.focus {
			card.ScrollIntoView()
			pending.focus = false
		}
		card.Children(func() {
			label(c, pending.Title, 12, p.text).FontWeight(600).SingleLine().Tooltip(pending.Title)
			label(c, fmt.Sprintf("本次操作 %d 项 · 准备中 %d", len(items), len(items)), 11, p.muted).Wrap()
			for index, item := range items {
				if index > 0 {
					ui.Divider(c)
				}
				ui.Column(c.Key(item.selectionKey)).FillWidth().MinWidth(0).Padding(5, 0).Gap(4).Label("Tag 准备条目 · " + pending.Nonce + ":" + item.selectionKey).Children(func() {
					ui.Row(c).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
						name := item.serviceName
						if item.moduleName != item.serviceName {
							name += " · " + item.moduleName
						}
						label(c, name, 13, p.text).FontWeight(600).Grow(1).MinWidth(0).SingleLine().Tooltip(name)
						label(c, "准备构建", 11, p.blue).Shrink(0).NoWrap()
					})
					branches := item.sourceBranch + " → " + firstNonEmpty(item.targetBranch, "目标待确认")
					label(c, branches, 11, p.muted).FillWidth().MinWidth(0).SingleLine().Tooltip(branches)
				})
			}
		})
	}
	return visible
}
