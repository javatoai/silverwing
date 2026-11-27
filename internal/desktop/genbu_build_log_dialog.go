package desktop

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
	code "github.com/egoist/mygo/ui/editor"
)

const (
	genbuBuildLogDefaultTail  = 1000
	genbuBuildLogPollInterval = 2 * time.Second
)

type genbuBuildLogDialogState struct {
	open, loaded, followTail               bool
	current                                func() bool
	action                                 application.Action
	serviceName, genbuService, environment string
	tag, tail, text, logError, tailError   string
	tailLines                              int
	nextPoll                               time.Time
	logRequest                             *genbuReadRequest
	preview                                nativeCodePreviewState
}

func (s *app) openGenbuBuildLog(serviceName, genbuService, environment, tag string, current func() bool, action application.Action) {
	if !s.integrationReady("genbu") {
		return
	}
	s.closeGenbuReadDialogs()
	d := &genbuBuildLogDialogState{open: true, followTail: true, current: current, action: action,
		serviceName: serviceName, genbuService: genbuService, environment: environment, tag: tag, tail: strconv.Itoa(genbuBuildLogDefaultTail)}
	s.genbuBuildLog = d
	s.readGenbuBuildLog(d)
}

func (s *app) genbuBuildLogCurrent(d *genbuBuildLogDialogState) bool {
	return d != nil && d.open && s.genbuBuildLog == d && s.integrationReady("genbu") && d.current() && !s.taskServicesUnit.stopped
}

func genbuLogTail(value string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 1 || n > 1000 {
		return 0, fmt.Errorf("日志行数应为 1 到 1000")
	}
	return n, nil
}

func (s *app) readGenbuBuildLog(d *genbuBuildLogDialogState) {
	if !s.genbuBuildLogCurrent(d) {
		return
	}
	tail, err := genbuLogTail(d.tail)
	if err != nil {
		d.tailError = err.Error()
		return
	}
	d.tail, d.tailLines, d.tailError = strconv.Itoa(tail), tail, ""
	s.requestGenbuBuildLog(d)
}

func (s *app) pollGenbuBuildLog(c *ui.Context, d *genbuBuildLogDialogState) {
	if !s.genbuBuildLogCurrent(d) {
		return
	}
	now := c.Now()
	if d.nextPoll.IsZero() {
		d.nextPoll = now.Add(genbuBuildLogPollInterval)
	} else if !now.Before(d.nextPoll) {
		d.nextPoll = now.Add(genbuBuildLogPollInterval)
		s.requestGenbuBuildLog(d)
	}
	c.After(d.nextPoll.Sub(now))
}

func (s *app) requestGenbuBuildLog(d *genbuBuildLogDialogState) {
	if !s.genbuBuildLogCurrent(d) || d.logRequest != nil {
		return
	}
	tail := d.tailLines
	request := d.action
	request.Payload = maps.Clone(request.Payload)
	request.Payload["tailLines"] = tail
	s.runGenbuRead(&d.logRequest, func() bool { return s.genbuBuildLogCurrent(d) }, request, func(result application.ActionResult, err error) {
		if tail != d.tailLines {
			s.requestGenbuBuildLog(d)
			return
		}
		data, err := genbuReadData[application.GenbuBuildLogResult](result, err)
		if err != nil {
			d.logError = err.Error()
			return
		}
		if data.Tag != d.tag || (d.genbuService != "" && data.Service != d.genbuService) {
			d.logError = "Genbu 返回的构建日志与当前服务或 Tag 不匹配"
			return
		}
		d.loaded, d.text, d.logError = true, data.Text, ""
	})
}

func (s *app) renderGenbuBuildLogDialog(c *ui.Context, p palette, d *genbuBuildLogDialogState) {
	scope := d.serviceName
	if d.environment != "" {
		scope += " · " + d.environment
	}
	scope += " · " + d.tag
	closeDialog := func() {
		if s.genbuBuildLog == d {
			s.closeGenbuReadDialogs()
		}
	}
	genbuReadDialog(c, p, "genbu-build-log", "构建日志", scope, &d.open, closeDialog, func(panel ui.Element) {
		if d.preview.view == nil {
			panel.OnShortcut(0, ui.KeyEscape, closeDialog)
		}
		followEnabled := false
		ui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			label(c, "末尾行数", 12, p.muted).NoWrap()
			ui.TextInput(c.Key("genbu-log-tail"), &d.tail).Width(74).Height(30).Label("末尾行数").OnSubmit(func() {
				s.readGenbuBuildLog(d)
			})
			readLabel := "读取日志"
			if d.loaded {
				readLabel = "刷新日志"
			}
			if ui.Button(c.Key("genbu-log-refresh"), readLabel).Disabled(d.logRequest != nil).Clicked() {
				s.readGenbuBuildLog(d)
			}
			if ui.Button(c.Key("genbu-log-find"), "查找").Disabled(d.preview.view == nil).Clicked() {
				d.preview.view.Run(code.Find)
			}
			if ui.Button(c.Key("genbu-log-copy"), "复制").Disabled(d.text == "").Clicked() {
				s.copyTextToClipboard(c.Services(), d.text, "已复制构建日志")
			}
			follow := ui.Checkbox(c.Key("genbu-log-follow"), &d.followTail, "跟随最后一行")
			followEnabled = follow.Changed() && d.followTail
		})
		s.pollGenbuBuildLog(c, d)
		if !d.loaded && d.logRequest != nil {
			label(c, "正在读取构建日志…", 12, p.muted).Wrap()
		}
		if message := firstNonEmpty(d.tailError, d.logError); message != "" {
			label(c, message, 12, p.danger).Wrap().Selectable()
		}
		if d.loaded && d.text != "" {
			changed := d.updateLogPreview()
			view := d.preview.view
			if d.followTail && (changed || followEnabled) {
				position := view.ScrollPosition()
				position.Y = view.LineTop(view.Buffer.LineCount() - 1)
				view.SetScrollPosition(position)
			}
			renderNativeCodePreviewView(c, p, view, "genbu-log-content").Grow(1).
				OnShortcut(0, ui.KeyEscape, closeDialog)
		} else if d.loaded {
			label(c, "该 Tag 暂无构建日志。", 12, p.muted).Wrap()
		}
	})
}

func (d *genbuBuildLogDialogState) updateLogPreview() bool {
	if d.preview.view == nil {
		view := d.preview.bind(d.action.Kind+"\x00"+d.tag, "构建日志", d.text)
		view.NoScrollPastEnd = true
		return true
	}
	if d.preview.source == d.text {
		return false
	}
	retained := d.preview.source
	d.preview.source = d.text
	view := d.preview.view
	buffer := view.Buffer

	// Polls can trim the first lines once the response reaches the requested
	// tail size. Edit around the retained lines so the editor can keep its
	// scroll anchor, selection and find matches with the text.
	skippedLines := 0
	for retained != "" && !strings.HasPrefix(d.text, retained) {
		end := strings.IndexAny(retained, "\r\n")
		if end == -1 {
			retained = ""
			break
		}
		if strings.HasPrefix(retained[end:], "\r\n") {
			end++
		}
		retained = retained[end+1:]
		skippedLines++
	}
	if retained != "" {
		end := buffer.Len()
		buffer.EditRemote(
			code.Edit{End: buffer.Offset(code.Pos{Line: skippedLines})},
			code.Edit{Start: end, End: end, Text: d.text[len(retained):]},
		)
	} else {
		position := view.ScrollPosition()
		buffer.Reset(d.text)
		view.SetScrollPosition(position)
	}
	return true
}
