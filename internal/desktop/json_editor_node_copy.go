package desktop

import (
	"silverwing-go/internal/tools"

	"github.com/egoist/mygo/ui"
)

// Source coordinates and window services survive a native menu callback;
// Elements belong to the frame and must never be queried from that callback.
type jsonNodeCopyRequest struct {
	source   string
	revision uint64
	node     tools.JSONSourceNode
	services ui.Services
}

func jsonEditorNodeCopyAtCaret(state *toolPageState, caret int, services ui.Services) (jsonNodeCopyRequest, bool) {
	if state == nil || !state.jsonValid || state.jsonPending || state.jsonEditor.sourceSnapshot != state.jsonSource {
		return jsonNodeCopyRequest{}, false
	}
	units := 0
	for index, point := range []rune(state.jsonSource) {
		if index >= caret {
			break
		}
		units++
		if point > 0xffff {
			units++
		}
	}
	var found tools.JSONSourceNode
	hasNode := false
	for _, node := range state.jsonDoc.Nodes {
		if units >= node.StartOffset && units < node.EndOffset && (!hasNode || node.EndOffset-node.StartOffset < found.EndOffset-found.StartOffset) {
			found, hasNode = node, true
		}
	}
	if !hasNode {
		for _, node := range state.jsonDoc.Nodes {
			if units == node.EndOffset {
				found, hasNode = node, true
				break
			}
		}
	}
	return jsonNodeCopyRequest{source: state.jsonSource, revision: state.jsonRevision, node: found, services: services}, hasNode
}

func jsonNodeCopyMatches(state *toolPageState, request *jsonNodeCopyRequest) bool {
	if state == nil || request == nil || !state.jsonValid || state.jsonPending ||
		state.jsonSource != request.source || state.jsonRevision != request.revision {
		return false
	}
	for _, node := range state.jsonDoc.Nodes {
		if node == request.node {
			return true
		}
	}
	return false
}

func (s *app) copyJSONEditorNode(state *toolPageState, command string, request *jsonNodeCopyRequest) {
	if s.toolsUnit.state.tools != state || !jsonNodeCopyMatches(state, request) {
		return
	}
	switch command {
	case "copy-node-fragment":
		start := tools.UTF16OffsetToByte(request.source, request.node.StartOffset)
		end := tools.UTF16OffsetToByte(request.source, request.node.EndOffset)
		s.copyTextToClipboard(request.services, request.source[start:end], "")
	case "copy-node-pointer":
		s.copyTextToClipboard(request.services, request.node.Pointer, "")
	case "copy-node-value":
		// Parsing large nodes belongs to the existing per-tool worker. Its
		// normal revision/source checks also reject a response after an edit.
		s.act("tool.json", "", "", "", "", request.source, map[string]any{
			"operation": "node-value", "revision": request.revision,
			"nodeStart": request.node.StartOffset, "nodeEnd": request.node.EndOffset,
		})
		if current := s.ensureDeveloperToolJobs().current["tool.json"]; current != nil &&
			current.action.Text == request.source && payloadString(current.action.Payload, "operation") == "node-value" &&
			payloadUint64(current.action.Payload, "revision") == request.revision {
			current.jsonNodeCopy = request
		}
	}
}
