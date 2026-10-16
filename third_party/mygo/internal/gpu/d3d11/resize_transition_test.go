//go:build windows

package d3d11

import (
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo/internal/scene"
)

func TestResizeBufferTransitionLatency(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	r, err := New(hiddenWindow(t, 960, 720))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	for i, count := range []int{2, 2, 3, 3, 2, 2, 3, 3} {
		w, h := 960+i*10, 720+i*10
		start := time.Now()
		if err := r.buffers(w, h, w, h, count); err != nil {
			t.Fatal(err)
		}
		bufferTime := time.Since(start)
		if err := r.drawScene(&scene.Scene{Width: w, Height: h, Clear: scene.Color{R: 244, G: 247, B: 251, A: 255}}, &r.b, r.rtv); err != nil {
			t.Fatal(err)
		}
		if err := r.present(0); err != nil {
			t.Fatal(err)
		}
		t.Logf("step=%d buffers=%d resize=%s total=%s", i, count, bufferTime, time.Since(start))
	}
}
