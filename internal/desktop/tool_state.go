package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/tools"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

type jsonToolState struct {
	viewSource        string
	sourceSnapshot    string
	projection        jsonTextProjection
	folded            map[string]bool
	finding           bool
	findQuery         string
	lastFindQuery     string
	findIndex         int
	findChanged       bool
	lines             jsonNativeLines
	scroll            ui.ScrollState
	native            codeeditor.State
	nativeSource      string
	nativeVersion     uint64
	nativeNodeMenu    *jsonNodeCopyRequest
	foldCache         map[int]tools.JSONSourceNode
	foldCacheSource   string
	foldCacheView     string
	foldCacheRevision uint64
	foldCacheValid    bool
	history           []jsonEditorSnapshot
	historyIndex      int
	historyReady      bool
	lastSelection     jsonEditorSnapshot
	pendingSelection  bool
	pendingFocus      bool
	selectionStart    int
	selectionEnd      int
}

// toolPageState keeps the developer tools independent from the legacy task
// input map. Revisions are carried through asynchronous actions so an old
// response can never replace a newer edit.
type toolPageState struct {
	activeTab string

	jsonSource             string
	jsonDoc                tools.JSONDocument
	jsonValid              bool
	jsonPending            bool
	jsonError              string
	jsonErrorLine          int
	jsonErrorColumn        int
	jsonEditor             jsonToolState
	jsonRevision           uint64
	jsonValidationCancel   context.CancelFunc
	jsonErrorLocatePending bool

	timeInput    string
	timeZone     string
	timeUnit     string
	timeResult   tools.TimeConversion
	timeHasData  bool
	timePending  bool
	timeError    string
	timeRevision uint64
	timeCancel   context.CancelFunc

	diffLeft                string
	diffRight               string
	diffOptions             tools.DiffOptions
	diffAuto                bool
	diffHideInputs          bool
	diffWrap                bool
	diffNative              *diffNativeViewState
	diffInputHistory        [2]diffInputHistory
	diffCollapseEqual       bool
	diffExpandedEqual       map[int]bool
	diffResult              tools.DiffResult
	diffHasData             bool
	diffPending             bool
	diffError               string
	diffRevision            uint64
	diffCancel              context.CancelFunc
	diffActiveHunk          int
	diffScrollRow           int
	diffScrollPending       bool
	diffOutputRevision      uint64
	diffSplitRatio          float32
	diffSharedScrollSide    int
	diffObservedScrollY     [2]float64
	diffObservedScrollReady bool

	portFilter           string
	portInputError       string
	portItems            []tools.PortProcess
	portShowTCP          bool
	portShowUDP          bool
	portSelected         int
	portConfirm          bool
	portHasQuery         bool
	portRequest          uint64
	portFilterRevision   uint64
	portLastStatus       string
	portLastMessage      string
	portStoppingPID      int
	portStopSequence     uint64
	portStoppingSequence uint64
}

func newToolPageState(source string) *toolPageState {
	state := &toolPageState{
		activeTab:         "JSON",
		jsonSource:        source,
		timeZone:          "Asia/Shanghai",
		timeUnit:          "AUTO",
		diffOptions:       tools.DiffOptions{ContextLines: 3},
		diffAuto:          true,
		diffWrap:          true,
		diffSplitRatio:    0.5,
		diffCollapseEqual: true,
		diffExpandedEqual: make(map[int]bool),
		portItems:         []tools.PortProcess{},
		portShowTCP:       true,
		portShowUDP:       true,
		portSelected:      -1,
	}
	state.jsonRevision = 1
	initializeJSONEditor(state)
	state.validateJSON(source)
	return state
}

func (s *app) ensureToolState() *toolPageState {
	if s.tools == nil {
		s.tools = newToolPageState(s.json)
	}
	return s.tools
}

func (s *app) queueJSONValidation() {
	state := s.ensureToolState()
	s.cancelDeveloperToolJob("tool.json")
	if state.jsonValidationCancel != nil {
		state.jsonValidationCancel()
		state.jsonValidationCancel = nil
	}
	state.jsonRevision++
	revision, source := state.jsonRevision, state.jsonSource
	state.jsonDoc = tools.JSONDocument{}
	state.jsonErrorLocatePending = false
	state.jsonError = ""
	state.jsonErrorLine, state.jsonErrorColumn = 0, 0
	if strings.TrimSpace(source) == "" {
		state.jsonPending = false
		state.jsonValid = false
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	state.jsonValidationCancel = cancel
	state.jsonPending = true
	state.jsonValid = false
	go func() {
		timer := time.NewTimer(260 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		doc, err := tools.ParseJSON(source)
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			if s.tools != state || state.jsonRevision != revision || state.jsonSource != source || ctx.Err() != nil {
				return
			}
			if err != nil {
				state.jsonDoc = tools.JSONDocument{}
				state.jsonValid = false
				state.jsonPending = false
				state.setJSONError(err)
				return
			}
			state.jsonDoc = doc
			state.jsonValid = true
			state.jsonPending = false
			state.jsonError = ""
		})
	}()
}

func (s *app) queueTimeConversion() {
	state := s.ensureToolState()
	s.cancelDeveloperToolJob("tool.time")
	if state.timeCancel != nil {
		state.timeCancel()
		state.timeCancel = nil
	}
	state.timeRevision++
	revision, source, unit, zone := state.timeRevision, state.timeInput, state.timeUnit, state.timeZone
	state.timeError = ""
	if tools.TimeInputBlank(source) {
		state.timeHasData = false
		state.timePending = false
		return
	}
	state.timeHasData = false
	state.timePending = true
	ctx, cancel := context.WithCancel(context.Background())
	state.timeCancel = cancel
	go func() {
		timer := time.NewTimer(300 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		result, err := tools.ConvertTime(source, unit, zone)
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			if s.tools != state || state.timeRevision != revision || state.timeInput != source || state.timeZone != zone || ctx.Err() != nil {
				return
			}
			state.timePending = false
			state.timeCancel = nil
			if err != nil {
				state.timeHasData = false
				state.timeError = err.Error()
				return
			}
			state.timeResult = result
			state.timeHasData = true
			state.timeError = ""
		})
	}()
}

func (s *app) queueDiff() {
	state := s.ensureToolState()
	s.cancelDeveloperToolJob("tool.diff")
	state.diffRevision++
	revision, left, right, options := state.diffRevision, state.diffLeft, state.diffRight, state.diffOptions
	state.diffHasData = false
	state.diffPending = false
	state.diffError = ""
	state.diffExpandedEqual = make(map[int]bool)
	state.diffActiveHunk = 0
	if state.diffCancel != nil {
		state.diffCancel()
		state.diffCancel = nil
	}
	if !state.diffAuto || left == "" && right == "" {
		return
	}
	state.diffPending = true
	ctx, cancel := context.WithCancel(context.Background())
	state.diffCancel = cancel
	go func() {
		timer := time.NewTimer(280 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		result, err := tools.CompareText(ctx, left, right, options)
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			if s.tools != state || state.diffRevision != revision || state.diffLeft != left || state.diffRight != right || ctx.Err() != nil {
				return
			}
			state.diffPending = false
			state.diffCancel = nil
			if err != nil {
				state.diffError = err.Error()
				state.diffHasData = false
				return
			}
			state.diffResult = result
			state.diffHasData = true
			state.diffOutputRevision = revision
			state.diffError = ""
		})
	}()
}

func (s *toolPageState) handleResult(action application.Action, result application.ActionResult) {
	switch action.Kind {
	case "tool.json":
		if payloadUint64(action.Payload, "revision") != s.jsonRevision {
			return
		}
		if s.jsonValidationCancel != nil {
			s.jsonValidationCancel()
			s.jsonValidationCancel = nil
		}
		s.commitProgrammaticJSONSource(result.Text)
		s.jsonRevision++
		s.jsonErrorLocatePending = false
		s.validateJSON(result.Text)
	case "tool.time":
		if payloadUint64(action.Payload, "revision") != s.timeRevision {
			return
		}
		s.timePending = false
		if converted, ok := actionData[tools.TimeConversion](result.Data); ok {
			s.timeResult = converted
			s.timeHasData = true
			s.timeError = ""
		}
	case "tool.diff":
		if payloadUint64(action.Payload, "revision") != s.diffRevision {
			return
		}
		s.diffPending = false
		if compared, ok := actionData[tools.DiffResult](result.Data); ok {
			s.diffResult = compared
			s.diffHasData = true
			s.diffError = ""
			s.diffOutputRevision = s.diffRevision
			s.diffExpandedEqual = make(map[int]bool)
			s.diffActiveHunk = 0
		}
	case "tool.ports":
		if payloadUint64(action.Payload, "request") != s.portRequest || payloadUint64(action.Payload, "filterRevision") != s.portFilterRevision {
			return
		}
		items, err := actionDataSlice[tools.PortProcess](result.Data)
		if err != nil {
			s.portItems = []tools.PortProcess{}
			s.portSelected = -1
			s.portConfirm = false
			s.portLastMessage = "端口结果无法读取：" + err.Error()
			return
		}
		s.portItems = items
		s.portSelected = -1
		s.portConfirm = false
		s.portHasQuery = true
		s.portLastStatus = ""
		s.portLastMessage = ""
	case "tool.port-stop":
		item, validIdentity := actionData[tools.PortProcess](action.Payload)
		if !validIdentity {
			return
		}
		stopRequest := payloadUint64(action.Payload, "stopRequest")
		if stopRequest != 0 && stopRequest != s.portStoppingSequence {
			return
		}
		outcome, ok := actionData[tools.PortTerminationResult](result.Data)
		if !ok || outcome.Status == "" {
			s.portLastStatus = "UNKNOWN"
			s.portLastMessage = firstNonEmpty(result.Message, "停止结果未包含状态，请刷新端口确认。")
			s.portConfirm = false
			s.clearPortStopping(item, stopRequest)
			return
		}
		if payloadUint64(action.Payload, "request") == s.portRequest && payloadUint64(action.Payload, "filterRevision") == s.portFilterRevision {
			s.portItems = outcome.Processes
			if s.portItems == nil {
				s.portItems = []tools.PortProcess{}
			}
			s.portSelected = -1
			s.portHasQuery = true
		}
		s.portConfirm = false
		s.portLastStatus = outcome.Status
		s.portLastMessage = firstNonEmpty(outcome.Message, result.Message, "停止操作已返回结果。")
		s.clearPortStopping(item, stopRequest)
	}
}

func (s *toolPageState) clearPortStopping(item tools.PortProcess, sequence uint64) {
	if sequence == 0 || sequence == s.portStoppingSequence {
		if s.portStoppingPID == item.PID {
			s.portStoppingPID = 0
			s.portStoppingSequence = 0
		}
	}
}

func (s *toolPageState) validateJSON(source string) {
	if strings.TrimSpace(source) == "" {
		s.jsonDoc = tools.JSONDocument{}
		s.jsonPending = false
		s.jsonValid = false
		s.jsonError = ""
		s.jsonErrorLine, s.jsonErrorColumn = 0, 0
		return
	}
	doc, err := tools.ParseJSON(source)
	s.jsonPending = false
	if err != nil {
		s.jsonDoc = tools.JSONDocument{}
		s.jsonValid = false
		s.setJSONError(err)
		return
	}
	s.jsonDoc = doc
	s.jsonValid = true
	s.jsonError = ""
	s.jsonErrorLine, s.jsonErrorColumn = 0, 0
}

func (s *toolPageState) setJSONError(err error) {
	s.jsonError = err.Error()
	s.jsonErrorLine, s.jsonErrorColumn = 0, 0
	var detail *tools.JSONError
	if errors.As(err, &detail) {
		s.jsonErrorLine, s.jsonErrorColumn = detail.Line, detail.Column
	}
}

func (s *toolPageState) cancelWork() {
	if s.jsonValidationCancel != nil {
		s.jsonValidationCancel()
		s.jsonValidationCancel = nil
	}
	s.jsonPending = false
	if s.timeCancel != nil {
		s.timeCancel()
		s.timeCancel = nil
	}
	s.timePending = false
	if s.diffCancel != nil {
		s.diffCancel()
		s.diffCancel = nil
	}
	s.diffPending = false
}

func actionData[T any](data any) (T, bool) {
	if value, ok := data.(T); ok {
		return value, true
	}
	var value T
	if data == nil {
		return value, false
	}
	raw, err := json.Marshal(data)
	if err != nil || json.Unmarshal(raw, &value) != nil {
		return value, false
	}
	return value, true
}

func actionDataSlice[T any](data any) ([]T, error) {
	if values, ok := data.([]T); ok {
		return values, nil
	}
	if data == nil {
		return []T{}, nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var values []T
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	if values == nil {
		values = []T{}
	}
	return values, nil
}

func payloadUint64(payload map[string]any, key string) uint64 {
	switch value := payload[key].(type) {
	case int:
		if value > 0 {
			return uint64(value)
		}
	case int64:
		if value > 0 {
			return uint64(value)
		}
	case uint64:
		return value
	case uint:
		return uint64(value)
	case float64:
		if value > 0 {
			return uint64(value)
		}
	case json.Number:
		if value, err := value.Int64(); err == nil && value > 0 {
			return uint64(value)
		}
	}
	return 0
}

func samePortProcess(left, right tools.PortProcess) bool {
	return left.Protocol == right.Protocol && left.Address == right.Address && left.Port == right.Port &&
		left.PID == right.PID && left.StartedAt == right.StartedAt && left.Path == right.Path
}

func (s *toolPageState) currentTime() time.Time { return time.Now() }
