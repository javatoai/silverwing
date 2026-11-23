package ui

import "unsafe"

const nodeSize = int(unsafe.Sizeof(node{}))

// builtSnapshot is what the view built.
type builtSnapshot struct {
	nodes  []*node
	bytes  []byte
	hashes []uint64
	// order sorts nodes by address, to find the node of a journal entry.
	order   []int
	scratch []byte
}

// holds compares the node at i with the address p, for a binary search.
func (s *builtSnapshot) holds(i int, p uintptr) int {
	a := uintptr(unsafe.Pointer(s.nodes[i]))
	switch {
	case p < a:
		return 1
	case p >= a+uintptr(nodeSize):
		return -1
	}
	return 0
}

func (s *builtSnapshot) byAddress(a, b int) int {
	x, y := uintptr(unsafe.Pointer(s.nodes[a])), uintptr(unsafe.Pointer(s.nodes[b]))
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}
