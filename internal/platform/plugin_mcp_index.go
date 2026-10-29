package platform

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

type pluginMCPManifestKey struct {
	name, source string
}

type pluginMCPManifest struct {
	path     string
	manifest map[string]any
	mtime    time.Time
}

// EqualFold, used by discovery before indexing, includes Unicode simple folds
// such as sigma and the Kelvin sign. Use the same equivalence classes as keys.
func pluginMCPSourceKey(source string) string {
	return strings.Map(func(value rune) rune {
		key := value
		for folded := unicode.SimpleFold(value); folded != value; folded = unicode.SimpleFold(folded) {
			key = min(key, folded)
		}
		return key
	}, source)
}

// The index belongs to one discovery operation. A manifest is read and decoded
// once, while source-qualified and unqualified names retain the original
// newest-mtime choice (and first path on a tie).
func indexPluginMCPManifests(cache string, read func(string, int64) ([]byte, error)) map[pluginMCPManifestKey]pluginMCPManifest {
	index := make(map[pluginMCPManifestKey]pluginMCPManifest)
	candidates := 0
	_ = filepath.WalkDir(cache, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		relative, relErr := filepath.Rel(cache, path)
		if relErr != nil {
			return nil
		}
		if entry.IsDir() {
			if path != cache && strings.Count(filepath.ToSlash(relative), "/") > 6 {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "plugin.json" || filepath.Base(filepath.Dir(path)) != ".codex-plugin" || !isRegular(path) {
			return nil
		}
		candidates++
		data, readErr := read(path, maxPluginBytes)
		var manifest map[string]any
		if readErr == nil && json.Unmarshal(data, &manifest) == nil {
			name := firstString(manifest, "name")
			info, _ := os.Stat(path)
			candidate := pluginMCPManifest{path: path, manifest: manifest}
			if info != nil {
				candidate.mtime = info.ModTime()
			}
			source := pluginMCPSourceKey(strings.Split(relative, string(os.PathSeparator))[0])
			for _, key := range []pluginMCPManifestKey{{name: name}, {name: name, source: source}} {
				if previous, found := index[key]; !found || candidate.mtime.After(previous.mtime) {
					index[key] = candidate
				}
			}
		}
		if candidates >= 500 {
			return filepath.SkipAll
		}
		return nil
	})
	return index
}
