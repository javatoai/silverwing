package desktop

// Captures use the real reading/layout state without restoring or writing the
// user's browsing cache. A nil store makes this session entirely in memory.
func newMemoryReadingSession(home string) *desktopReadingSession {
	return &desktopReadingSession{
		home: home, cache: EmptyReadingSnapshot(), loaded: true,
		restoreAttempted:  true,
		restoredMaterials: make(map[string]bool),
		scrollStates:      make(map[string]readingScrollBinding),
	}
}
