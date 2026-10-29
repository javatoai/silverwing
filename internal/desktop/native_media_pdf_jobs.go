package desktop

import (
	"context"
	"math"
	"sort"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

const pdfRenderJobTraceLimit = 32
const pdfRenderStageEventLimit = 12

// The UI can invalidate a completed request while its result is still queued.
// Keep that signal separate from releasing the worker's context resources.
type pdfPageRenderRequest struct {
	cancel      context.CancelFunc
	invalidated atomic.Bool
}

func (r *pdfPageRenderRequest) invalidate() {
	r.invalidated.Store(true)
	r.cancel()
}

func (r *pdfPageRenderRequest) accepts(s *app, m *nativeMediaState, window *mygo.Window, pageIndex, widthPx int, cancelled bool) bool {
	if r == nil || cancelled || r.invalidated.Load() || s == nil || m == nil || m.closed || m.kind != "pdf" || s.materialsUnit.state.nativeMedia != m || s.win != window || window == nil || window.IsDestroyed() || pageIndex < 0 || pageIndex >= m.pdfInfo.PageCount {
		return false
	}
	currentWidth := maxInt(1, int(math.Round(float64(pdfPageDisplayWidth(m.viewerWidth, m.zoom)))))
	return widthPx == currentWidth
}

type pdfPageRenderStageEvent struct {
	Stage string    `json:"stage"`
	At    time.Time `json:"at"`
	Error string    `json:"error,omitempty"`
}

type pdfPageRenderJobTrace struct {
	Key       string                    `json:"key"`
	PageIndex int                       `json:"pageIndex"`
	WidthPx   int                       `json:"widthPx"`
	StartedAt time.Time                 `json:"startedAt"`
	UpdatedAt time.Time                 `json:"updatedAt"`
	Events    []pdfPageRenderStageEvent `json:"events"`
}

func recordPDFPageRenderStage(m *nativeMediaState, key string, pageIndex, widthPx int, stage string, err error) {
	if m == nil || key == "" || stage == "" {
		return
	}

	now := time.Now().UTC()
	m.pdfRenderTraceMu.Lock()
	defer m.pdfRenderTraceMu.Unlock()
	if m.pdfRenderTraces == nil {
		m.pdfRenderTraces = make(map[string]pdfPageRenderJobTrace)
	}
	trace, ok := m.pdfRenderTraces[key]
	if stage == "render-start" || !ok {
		trace = pdfPageRenderJobTrace{Key: key, PageIndex: pageIndex, WidthPx: widthPx, StartedAt: now}
	}
	event := pdfPageRenderStageEvent{Stage: stage, At: now}
	if err != nil {
		event.Error = err.Error()
	}
	trace.UpdatedAt = now
	if len(trace.Events) < pdfRenderStageEventLimit {
		trace.Events = append(trace.Events, event)
	}
	m.pdfRenderTraces[key] = trace
	prunePDFPageRenderTraces(m.pdfRenderTraces)
}

func snapshotPDFPageRenderTraces(m *nativeMediaState) []pdfPageRenderJobTrace {
	if m == nil {
		return nil
	}
	m.pdfRenderTraceMu.Lock()
	defer m.pdfRenderTraceMu.Unlock()
	if len(m.pdfRenderTraces) == 0 {
		return nil
	}

	keys := make([]string, 0, len(m.pdfRenderTraces))
	for key := range m.pdfRenderTraces {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	traces := make([]pdfPageRenderJobTrace, 0, len(keys))
	for _, key := range keys {
		trace := m.pdfRenderTraces[key]
		trace.Events = append([]pdfPageRenderStageEvent(nil), trace.Events...)
		traces = append(traces, trace)
	}
	return traces
}

func prunePDFPageRenderTraces(traces map[string]pdfPageRenderJobTrace) {
	if len(traces) <= pdfRenderJobTraceLimit {
		return
	}
	keys := make([]string, 0, len(traces))
	for key := range traces {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := traces[keys[i]], traces[keys[j]]
		if left.UpdatedAt.Equal(right.UpdatedAt) {
			return keys[i] < keys[j]
		}
		return left.UpdatedAt.Before(right.UpdatedAt)
	})
	for _, key := range keys[:len(keys)-pdfRenderJobTraceLimit] {
		delete(traces, key)
	}
}
