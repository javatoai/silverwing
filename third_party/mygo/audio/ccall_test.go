//go:build darwin || (linux && (amd64 || arm64))

package audio

import (
	"runtime"
	"testing"

	"github.com/ebitengine/purego"
)

func TestCCallDoesNotAllocate(t *testing.T) {
	name := "libc.so.6"
	if runtime.GOOS == "darwin" {
		name = "/usr/lib/libSystem.B.dylib"
	}
	lib, err := purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Skip(err)
	}
	labs, err := purego.Dlsym(lib, "labs")
	if err != nil {
		t.Fatal(err)
	}
	var c cCaller
	minus5 := -5
	if got := int(c.call(labs, uintptr(minus5))); got != 5 {
		t.Fatalf("labs(-5) = %d", got)
	}
	if n := testing.AllocsPerRun(100, func() { c.call(labs, 7) }); n != 0 {
		t.Errorf("a call allocated %v times", n)
	}
}
