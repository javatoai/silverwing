package domain

import "strings"

// MaterialsSyncItemCanOverwrite identifies conflicts whose recorded resource
// and observed remote snapshot support an explicit local overwrite.
func MaterialsSyncItemCanOverwrite(item MaterialsSyncItem) bool {
	return item.Action == MaterialsSyncConflict && item.Kind != MaterialsSyncDirectory &&
		item.ObservedNode != nil && item.ObservedVersion != nil &&
		strings.TrimSpace(item.LocalHash) != "" && item.Entry != nil && item.Entry.Kind == item.Kind &&
		item.Path != "" && !strings.HasPrefix(item.Path, "/")
}
