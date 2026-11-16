package desktop

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
	"silverwing-go/internal/asyncjob"
	"silverwing-go/internal/tools"
)

const textCountDelay = 150 * time.Millisecond

type base64ToolState struct {
	input, output codeeditor.State
	inputVersion  uint64
	urlSafe       bool
	result        string
	err           string
	work          utilityTextWork
}

type textCountToolState struct {
	input        codeeditor.State
	inputVersion uint64
	result       tools.TextCounts
	dirty        bool
	due          time.Time
	err          string
	work         utilityTextWork
}

type utilityTextWork struct {
	lease      *asyncjob.Lease
	completion chan func()
	revision   uint64
}

func (w *utilityTextWork) invalidate() {
	w.revision++
	if w.lease != nil {
		w.lease.Cancel()
	}
}

func (w *utilityTextWork) poll() {
	select {
	case apply := <-w.completion:
		w.lease, w.completion = nil, nil
		apply()
	default:
	}
}

// Each tool keeps one bounded operation in flight. Background work publishes
// into a single slot and the view consumes it on the UI thread.
func startUtilityTextWork[T any](c *ui.Context, s *app, state *toolPageState, tab string, w *utilityTextWork, compute func() (T, error), apply func(T, error)) bool {
	if w.lease != nil || !s.utilityToolVisible(state, tab) {
		return false
	}
	lease, err := s.ensureDeveloperToolJobs().lifecycle.Begin(context.Background(), asyncjob.Options{CancelOnClose: true})
	if err != nil {
		return false
	}
	completion := make(chan func(), 1)
	w.lease, w.completion = lease, completion
	revision := w.revision
	services := c.Services()
	go func() {
		defer lease.Complete(nil)
		defer lease.Cancel()
		var value T
		var err error
		if lease.Context().Err() == nil {
			value, err = compute()
		}
		completion <- func() {
			if w.revision == revision && s.utilityToolVisible(state, tab) {
				apply(value, err)
			}
		}
		services.Invalidate()
	}()
	return true
}

func (s *app) utilityToolVisible(state *toolPageState, tab string) bool {
	return s.page == "tools" && s.toolsUnit.state.tools == state && state.activeTab == tab && s.developerToolTabVisible(tab)
}

func newUtilityTextEditor(label, placeholder string, maxBytes int, readOnly bool) codeeditor.State {
	buffer := codeeditor.NewBufferPreservingLineEndings("")
	buffer.SetMaxSourceBytes(maxBytes)
	return codeeditor.State{Buffer: buffer, Label: label, Placeholder: placeholder, LineNumbers: codeeditor.LineNumbersOff,
		ReadOnly: readOnly, SoftWrap: true, NoAutoClose: true, NoFolding: true, NoIndentGuides: true, NoScrollPastEnd: true}
}

func (s *app) utilityClipboardWriter(services ui.Services) func(string) bool {
	return func(text string) bool { return s.copyTextToClipboard(services, text, "") }
}

func renderUtilityEditor(c *ui.Context, p palette, key, title string, state *codeeditor.State) {
	ui.Column(c.Key(key+"-panel")).Grow(1).MinWidth(0).MinHeight(0).Padding(12).Gap(8).
		Background(p.surface).Border(1, p.line).Radius(10).Children(func() {
		label(c, title, 13, p.text).FontWeight(600)
		TextEditor(c.Key(key), p, state).FillWidth().Grow(1).MinWidth(0).MinHeight(0)
	})
}

func (b *base64ToolState) invalidate() {
	b.work.invalidate()
	b.result, b.err = "", ""
	if b.output.Buffer != nil && b.output.Buffer.Len() != 0 {
		// A new output buffer avoids accumulating a history of read-only results.
		b.output.Buffer = codeeditor.NewBufferPreservingLineEndings("")
	}
}

func (b *base64ToolState) setInput(value string) {
	b.input.Buffer.Set(value)
	b.inputVersion = b.input.Buffer.Version()
	b.invalidate()
}

func (s *app) runBase64Tool(c *ui.Context, state *toolPageState, encode bool) {
	b := &state.base64Tool
	if b.work.lease != nil {
		return
	}
	b.invalidate()
	source, urlSafe := b.input.Buffer.OriginalText(), b.urlSafe
	inputVersion := b.input.Buffer.Version()
	startUtilityTextWork(c, s, state, "Base64", &b.work, func() (string, error) {
		if encode {
			return tools.EncodeBase64(source, urlSafe, true)
		}
		return tools.DecodeBase64(source)
	}, func(result string, err error) {
		if inputVersion != b.input.Buffer.Version() {
			return
		}
		if err != nil {
			b.err = err.Error()
			return
		}
		b.result = result
		b.output.Buffer = codeeditor.NewBufferPreservingLineEndings(result)
	})
}

func (s *app) renderBase64Tool(c *ui.Context, p palette, state *toolPageState) {
	b := &state.base64Tool
	if b.input.Buffer == nil {
		b.input = newUtilityTextEditor("Base64 输入", "输入原文或粘贴 Base64", tools.MaxBase64InputBytes, false)
		b.output = newUtilityTextEditor("Base64 结果", "转换结果将在这里显示", tools.MaxBase64InputBytes, true)
		b.input.ClipboardWrite = s.utilityClipboardWriter(c.Services())
		b.output.ClipboardWrite = s.utilityClipboardWriter(c.Services())
		b.input.OnLimitExceeded = func() {
			b.invalidate()
			b.err = "输入不能超过 3 MiB，编码原文不能超过 2 MiB。"
		}
	}
	if b.inputVersion != b.input.Buffer.Version() {
		b.inputVersion = b.input.Buffer.Version()
		b.invalidate()
	}
	b.work.poll()
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Gap(10).Children(func() {
		ui.Row(c.Key("base64-actions")).FillWidth().Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
			canConvert := b.input.Buffer.Len() > 0 && b.work.lease == nil
			if toolButtonWithEnabled(c, "base64-encode", "编码", p, true, canConvert) {
				s.runBase64Tool(c, state, true)
			}
			if toolButtonWithEnabled(c, "base64-decode", "解码", p, false, canConvert) {
				s.runBase64Tool(c, state, false)
			}
			label(c, "编码格式", 12, p.muted)
			if toolToggle(c, "base64-standard", "标准", !b.urlSafe, p) && b.urlSafe {
				b.urlSafe = false
				b.invalidate()
			}
			if toolToggle(c, "base64-url", "URL-safe", b.urlSafe, p) && !b.urlSafe {
				b.urlSafe = true
				b.invalidate()
			}
			if toolButtonWithEnabled(c, "base64-copy", "复制结果", p, false, b.result != "") {
				s.copyTextToClipboard(c.Services(), b.result, "结果已复制")
			}
			if toolButtonWithEnabled(c, "base64-reuse", "结果用作输入", p, false, b.result != "") {
				b.setInput(b.result)
			}
			if toolButtonWithEnabled(c, "base64-clear", "清空", p, false, b.input.Buffer.Len() > 0 || b.err != "") {
				b.setInput("")
			}
		})
		if b.err != "" {
			label(c, b.err, 12, p.danger).Wrap()
		} else if b.work.lease != nil {
			label(c, "正在转换…", 12, p.muted)
		}
		width, _ := c.Size()
		var panes ui.Element
		if width >= 720 {
			panes = ui.Row(c.Key("base64-panes-side-by-side"))
		} else {
			panes = ui.Column(c.Key("base64-panes-stacked"))
		}
		panes.FillWidth().Grow(1).MinWidth(0).MinHeight(0).Gap(10).AlignItems(ui.Stretch).Children(func() {
			renderUtilityEditor(c, p, "base64-input", "输入", &b.input)
			renderUtilityEditor(c, p, "base64-output", "结果", &b.output)
		})
	})
}

func (t *textCountToolState) updateInput(now time.Time) {
	if version := t.input.Buffer.Version(); version != t.inputVersion {
		t.work.invalidate()
		t.inputVersion = version
		t.result, t.err = tools.TextCounts{}, ""
		t.dirty = t.input.Buffer.Len() != 0
		t.due = now.Add(textCountDelay)
	}
}

func (s *app) renderTextCountTool(c *ui.Context, p palette, state *toolPageState) {
	t := &state.textCountTool
	if t.input.Buffer == nil {
		t.input = newUtilityTextEditor("待统计文本", "输入或粘贴要统计的文字", tools.MaxTextBytes, false)
		t.input.ClipboardWrite = s.utilityClipboardWriter(c.Services())
		t.input.OnLimitExceeded = func() { t.err = developerToolInputSizeError }
	}
	t.updateInput(c.Now())
	t.work.poll()
	if t.dirty && t.work.lease == nil {
		if delay := t.due.Sub(c.Now()); delay > 0 {
			c.After(delay)
		} else {
			source, version := t.input.Buffer.OriginalText(), t.inputVersion
			startUtilityTextWork(c, s, state, "文字计数", &t.work, func() (tools.TextCounts, error) {
				return tools.CountText(source)
			}, func(result tools.TextCounts, err error) {
				if version != t.input.Buffer.Version() {
					return
				}
				t.dirty = false
				if err != nil {
					t.err = err.Error()
					return
				}
				t.result = result
			})
		}
	}
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Gap(10).Children(func() {
		ui.Row(c.Key("text-count-metrics")).FillWidth().Wrap().Gap(8).Children(func() {
			for _, metric := range []struct {
				key, title string
				value      int
			}{
				{"characters", "字符数", t.result.Characters}, {"non-whitespace", "非空白字符数", t.result.NonWhitespace},
				{"han", "汉字数", t.result.Han}, {"lines", "行数", t.result.Lines}, {"bytes", "UTF-8 字节数", t.result.Bytes},
			} {
				value := fmt.Sprint(metric.value)
				if t.dirty {
					value = "—"
				}
				ui.Column(c.Key("text-count-"+metric.key)).Grow(1).MinWidth(112).Padding(10, 12).Gap(3).
					Background(p.surface).Border(1, p.line).Radius(8).Label(metric.title + "：" + value).Children(func() {
					label(c, metric.title, 12, p.muted)
					label(c, value, 20, p.text).FontWeight(600)
				})
			}
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if t.err != "" {
				label(c, t.err, 12, p.danger).Wrap()
			} else if t.dirty {
				label(c, "正在统计…", 12, p.muted)
			}
			ui.Spacer(c)
			if toolButtonWithEnabled(c, "text-count-clear", "清空", p, false, t.input.Buffer.Len() > 0 || t.err != "") {
				t.input.Buffer.Set("")
				t.err = ""
				t.updateInput(c.Now())
			}
		})
		renderUtilityEditor(c, p, "text-count-input", "文本", &t.input)
	})
}
