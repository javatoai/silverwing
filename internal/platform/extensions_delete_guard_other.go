//go:build !windows

package platform

func ensureSafeDeleteAncestors(root, target string) error {
	return ensureNoSymlinksBeforeLeaf(root, target)
}
