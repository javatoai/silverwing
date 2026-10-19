package desktop

import (
	"math"
	"slices"
)

// Observe live UI inputs without walking or serializing historical documents.
// Explicit cache edits set dirty; native scroll state is checked independently
// because the framework updates it without going through application setters.
type readingObservationInputs struct {
	taskPath, root, requirementLink, documentPath  string
	tab                                            int
	documentsLoaded, documentsRequested, collapsed bool
	store                                          *ReadingStateStore
}

type readingObservation struct {
	valid   bool
	inputs  readingObservationInputs
	tabs    []string
	scrolls map[string]int
}

func (s *app) readingObservationInputs() readingObservationInputs {
	current := s.currentTask()
	return readingObservationInputs{
		taskPath: current.path, root: current.materialsPath, requirementLink: current.requirementURL,
		documentPath: s.documentPath, tab: s.tab, documentsLoaded: s.documentsLoaded,
		documentsRequested: s.documentsRequested, collapsed: s.documentsCollapsed, store: s.readingSession.store,
	}
}

func observedReadingScroll(binding readingScrollBinding) int {
	if binding.state == nil {
		return 0
	}
	return int(math.Round(float64(readingScrollCoordinate(binding.id, binding.state))))
}

func (o *readingObservation) unchanged(inputs readingObservationInputs, tabs []string, scrolls map[string]readingScrollBinding) bool {
	if !o.valid || o.inputs != inputs || !slices.Equal(o.tabs, tabs) || len(o.scrolls) != len(scrolls) {
		return false
	}
	for key, binding := range scrolls {
		if value, ok := o.scrolls[key]; !ok || value != observedReadingScroll(binding) {
			return false
		}
	}
	return true
}

func (o *readingObservation) remember(inputs readingObservationInputs, tabs []string, scrolls map[string]readingScrollBinding) {
	o.valid, o.inputs = true, inputs
	o.tabs = append(o.tabs[:0], tabs...)
	if o.scrolls == nil {
		o.scrolls = make(map[string]int, len(scrolls))
	}
	clear(o.scrolls)
	for key, binding := range scrolls {
		o.scrolls[key] = observedReadingScroll(binding)
	}
}
