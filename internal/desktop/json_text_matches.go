package desktop

import "unicode"

type jsonEditorMatch struct {
	start int
	end   int
}

// A single-entry cache retains only this editor's current source and query.
type jsonMatchCache struct {
	ready         bool
	source, query string
	matches       []jsonEditorMatch
}

func (cache *jsonMatchCache) get(source, query string) []jsonEditorMatch {
	if !cache.ready || cache.source != source || cache.query != query {
		cache.source, cache.query, cache.ready = source, query, true
		cache.matches = jsonEditorMatches(source, query)
	}
	return cache.matches
}

type jsonProjectedMatchCache struct {
	ready    bool
	revision uint64
	query    string
	matches  []jsonEditorMatch
	lfBytes  []jsonEditorMatch
}

func (cache *jsonProjectedMatchCache) update(projection jsonTextProjection, revision uint64, query string) {
	if cache.ready && cache.revision == revision && cache.query == query {
		return
	}
	cache.ready, cache.revision, cache.query = true, revision, query
	cache.matches = jsonEditorMatches(projection.text, query)
	cache.lfBytes = make([]jsonEditorMatch, len(cache.matches))
	for i, match := range cache.matches {
		cache.lfBytes[i] = jsonEditorMatch{start: projection.index.lfByteAtRune(match.start), end: projection.index.lfByteAtRune(match.end)}
	}
}

// Match original rune offsets with simple Unicode case folding and the existing
// non-overlapping find semantics. KMP avoids rescanning long repeated prefixes;
// source lowercasing is streamed rather than retaining another full rune copy.
func jsonEditorMatches(source, query string) []jsonEditorMatch {
	needle := []rune(query)
	if len(needle) == 0 {
		return nil
	}
	for i := range needle {
		needle[i] = unicode.ToLower(needle[i])
	}
	prefix := make([]int, len(needle))
	for i, k := 1, 0; i < len(needle); i++ {
		for k > 0 && needle[i] != needle[k] {
			k = prefix[k-1]
		}
		if needle[i] == needle[k] {
			k++
		}
		prefix[i] = k
	}
	var matches []jsonEditorMatch
	k, at := 0, 0
	for _, r := range source {
		r = unicode.ToLower(r)
		for k > 0 && r != needle[k] {
			k = prefix[k-1]
		}
		if r == needle[k] {
			k++
		}
		at++
		if k == len(needle) {
			matches = append(matches, jsonEditorMatch{start: at - len(needle), end: at})
			k = 0
		}
	}
	return matches
}
