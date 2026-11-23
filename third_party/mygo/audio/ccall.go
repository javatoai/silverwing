//go:build darwin || (linux && (amd64 || arm64))

package audio

import "github.com/ebitengine/purego"

// A cCaller calls C without allocating, for what a device calls on every
// write: functions RegisterLibFunc made allocate, and SyscallN's own
// arguments escape. Pointers among args must be to C memory, or to the
// heap and kept alive by the caller.
type cCaller struct{ args [6]uintptr }

// cFunc looks up C function name in lib, for a cCaller. As
// RegisterLibFunc does, it panics when lib lacks it.
func cFunc(lib uintptr, name string) uintptr {
	fn, err := purego.Dlsym(lib, name)
	if err != nil {
		panic(err)
	}
	return fn
}

func (c *cCaller) call(fn uintptr, args ...uintptr) uintptr {
	if len(args) > len(c.args) {
		panic("audio: too many arguments to cCaller.call")
	}
	n := copy(c.args[:], args)
	r, _, _ := purego.SyscallN(fn, c.args[:n]...)
	return r
}
