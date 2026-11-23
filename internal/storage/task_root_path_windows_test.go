//go:build windows

package storage

import "testing"

func TestValidateTaskRootRejectsJunctionOverlap(t *testing.T) {
	assertTaskRootAliasBoundary(t, func(link, target string) {
		manifestLookupJunction(t, link, target)
	})
}
