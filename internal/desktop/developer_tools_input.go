package desktop

import (
	"silverwing-go/internal/tools"

	"github.com/egoist/mygo/ui"
)

const developerToolInputSizeError = "单个文本输入不能超过 2 MiB"

func developerToolInputTooLarge(value string) bool {
	return len(value) > tools.MaxTextBytes
}

func jsonEditorCandidateSource(state *toolPageState, view string) string {
	start, end, inserted, _, _, _ := projectionEdit(state.jsonEditor.projection, view)
	return replaceRuneRange(state.jsonSource, start, end, inserted)
}

// Check the expanded source before the shared editor changes its document,
// folds, or undo history. A folded view alone can be smaller than the source.
func (s *app) applyJSONToolEditorChange(state *toolPageState, editor ui.Element) {
	if developerToolInputTooLarge(jsonEditorCandidateSource(state, state.jsonEditor.viewSource)) {
		s.rejectJSONToolInput(state)
		return
	}
	s.applyJSONEditorChange(state, editor)
}

// OnPaste can run between builds, when no Element is valid. The native editor
// applies the selection; OnChange checks the resulting candidate. Only a
// payload that already exceeds the limit can be rejected before insertion.
func (s *app) rejectJSONToolPaste(state *toolPageState, pasted string) bool {
	if !developerToolInputTooLarge(pasted) {
		return false
	}
	s.rejectJSONToolInput(state)
	return true
}

func (s *app) rejectJSONToolInput(state *toolPageState) {
	s.cancelDeveloperToolJob("tool.json")
	if state.jsonValidationCancel != nil {
		state.jsonValidationCancel()
		state.jsonValidationCancel = nil
	}
	state.jsonRevision++
	state.jsonPending = false
	state.jsonError = developerToolInputSizeError
	state.jsonErrorLine, state.jsonErrorColumn = 0, 0
	state.jsonErrorLocatePending = false
	state.jsonEditor.viewSource = state.jsonEditor.projection.text
}

func (s *app) acceptTimeToolInput(state *toolPageState, value string) bool {
	if developerToolInputTooLarge(value) {
		s.rejectTimeToolInput(state)
		return false
	}
	if state.timeInput == value {
		return false
	}
	state.timeInput = value
	return true
}

func (s *app) rejectTimeToolInput(state *toolPageState) {
	s.cancelDeveloperToolJob("tool.time")
	if state.timeCancel != nil {
		state.timeCancel()
		state.timeCancel = nil
	}
	state.timeRevision++
	state.timePending = false
	state.timeHasData = false
	state.timeResult = tools.TimeConversion{}
	state.timeError = developerToolInputSizeError
}

func (s *app) acceptDiffToolInput(state *toolPageState, target *string, value string) bool {
	if developerToolInputTooLarge(value) {
		s.rejectDiffToolInput(state)
		return false
	}
	if *target == value {
		return false
	}
	*target = value
	return true
}

func (s *app) rejectDiffToolInput(state *toolPageState) {
	s.cancelDeveloperToolJob("tool.diff")
	if state.diffCancel != nil {
		state.diffCancel()
		state.diffCancel = nil
	}
	state.diffRevision++
	state.diffPending = false
	state.diffError = developerToolInputSizeError
}
