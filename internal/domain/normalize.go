package domain

func NormalizeTaskManifest(manifest TaskManifest) TaskManifest {
	manifest.WorkspaceToolLaunches = cloneNonNullSlice(manifest.WorkspaceToolLaunches)
	return manifest
}

func cloneNonNullSlice[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	cloned := make([]T, len(values))
	copy(cloned, values)
	return cloned
}
