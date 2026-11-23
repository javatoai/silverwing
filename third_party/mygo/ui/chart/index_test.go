package chart

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"testing"
)

func TestMinMaxIndexReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(17, 42))
	for trial := 0; trial < 100; trial++ {
		var index MinMaxIndex
		var xs, ys, dx, dy []float32
		for frame := 0; frame < 40; frame++ {
			for range rng.IntN(40) {
				x := float32(0)
				if len(xs) > 0 {
					x = xs[len(xs)-1]
				}
				x += float32(rng.IntN(4))
				y := float32(rng.IntN(9) - 4)
				if rng.IntN(20) == 0 {
					y = float32(math.NaN())
				}
				if rng.IntN(40) == 0 {
					y = float32(math.Inf(2*rng.IntN(2) - 1))
				}
				xs = append(xs, x)
				ys = append(ys, y)
			}
			index.Update(xs, ys)
			lo := float32(rng.IntN(1000) - 200)
			hi := lo + float32(rng.IntN(1500)+1)
			width := []int{0, 1, 2, 16, 1280, 1600, 2560}[rng.IntN(7)]
			normalized := make([]float32, len(xs))
			for i, x := range xs {
				normalized[i] = (x - lo) / (hi - lo)
			}
			wx, wy := MinMax(nil, nil, normalized, ys, width)
			dx, dy = index.Query(dx, dy, lo, hi, width)
			equal := func(a, b float32) bool { return a == b || math.IsNaN(float64(a)) && math.IsNaN(float64(b)) }
			if !slices.EqualFunc(dx, wx, equal) || !slices.EqualFunc(dy, wy, equal) {
				t.Fatalf("trial %d frame %d [%g,%g] width %d\ngot %v %v\nwant %v %v", trial, frame, lo, hi, width, dx, dy, wx, wy)
			}
		}
		index.Reset()
		if index.xs != nil || index.levels != nil || index.runs != nil {
			t.Fatal("Reset retains storage")
		}
	}
}

func TestMinMaxIndexReuse(t *testing.T) {
	xs, ys := indexSignal(10000, "noise")
	var index MinMaxIndex
	index.Update(xs, ys)
	dx, dy := index.Query(nil, nil, 0, 9999, 1280)
	if a := testing.AllocsPerRun(20, func() { index.Update(xs, ys); dx, dy = index.Query(dx, dy, 0, 9999, 1280) }); a != 0 {
		t.Fatal(a)
	}
	index.Reset()
	index.Update(xs[:1], ys[:1])
	dx, dy = index.Query(dx, dy, 0, 1, 10)
	if len(dx) != 1 || dy[0] != ys[0] {
		t.Fatal(dx, dy)
	}
}

func TestMinMaxIndexDense(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 11))
	for _, kind := range []string{"sine", "noise", "spikes", "constant", "step", "duplicate", "uneven", "gapped"} {
		xs, ys := indexSignal(100000, kind)
		// Distinct X positions make every selected sample identifiable, even
		// when Y extrema tie. Duplicate X is checked separately by values.
		for _, width := range []int{1, 16, 1280, 1600, 2560, 3840} {
			var index MinMaxIndex
			for _, n := range []int{0, 1, 2, 15, 16, 17, 1023, 65535, 100000} {
				index.Update(xs[:n], ys[:n])
				for range 4 {
					lo := float32(rng.IntN(50000) - 10000)
					hi := lo + float32(rng.IntN(100000)+1)
					checkIndex(t, &index, xs[:n], ys[:n], lo, hi, width)
				}
			}
		}
	}
	for _, kind := range []string{"noise", "spikes"} {
		xs, ys := indexSignal(1000000, kind)
		var index MinMaxIndex
		index.Update(xs, ys)
		for _, width := range []int{1280, 1600, 2560, 3840} {
			for _, bounds := range [][2]float32{{0, 999999}, {.25, 999990.5}, {333333, 777777}, {990000, 1000001}} {
				checkIndex(t, &index, xs, ys, bounds[0], bounds[1], width)
			}
		}
	}
}

func checkIndex(t *testing.T, index *MinMaxIndex, xs, ys []float32, lo, hi float32, width int) {
	t.Helper()
	work := make([]float32, len(xs))
	for i, x := range xs {
		work[i] = (x - lo) / (hi - lo)
	}
	wx, wy := MinMax(nil, nil, work, ys, width)
	dx, dy := index.Query(nil, nil, lo, hi, width)
	equal := func(a, b float32) bool { return a == b || math.IsNaN(float64(a)) && math.IsNaN(float64(b)) }
	if !slices.EqualFunc(dx, wx, equal) || !slices.EqualFunc(dy, wy, equal) {
		t.Fatalf("reference mismatch: n=%d width=%d lo=%g hi=%g\ngot %v %v\nwant %v %v", len(xs), width, lo, hi, dx, dy, wx, wy)
	}
}

func TestMinMaxIndexEdges(t *testing.T) {
	inf, nan := float32(math.Inf(1)), float32(math.NaN())
	for _, xs := range [][]float32{nil, {0}, {-2, 2}, {-2, -1}, {2, 3}, {0, 0, 0, 0}, {0, nan, .5, inf, 1}, {-math.MaxFloat32, 0, math.MaxFloat32}} {
		ys := make([]float32, len(xs))
		for i := range ys {
			ys[i] = float32(i % 3)
		}
		var index MinMaxIndex
		index.Update(xs, ys)
		for _, bounds := range [][2]float32{{0, 1}, {-2, -1}, {1, 2}, {math.MaxFloat32 / 2, math.MaxFloat32}, {0, math.SmallestNonzeroFloat32}} {
			checkIndex(t, &index, xs, ys, bounds[0], bounds[1], 1)
		}
	}
}

func TestMinMaxIndexInvalidAndRebuild(t *testing.T) {
	var index MinMaxIndex
	xs, ys := []float32{0, .5, 1}, []float32{1, 2}
	index.Update(xs, ys)
	checkIndex(t, &index, xs, ys, 0, 1, 10)
	for _, bounds := range [][2]float32{{0, 0}, {1, 0}, {0, float32(math.NaN())}, {0, float32(math.Inf(1))}, {-math.MaxFloat32, math.MaxFloat32}} {
		dx, dy := index.Query(nil, nil, bounds[0], bounds[1], 10)
		if len(dx) != 0 || len(dy) != 0 {
			t.Fatal("invalid bounds produce output", bounds)
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("dropping samples without Reset must panic")
			}
		}()
		index.Update(xs[:1], ys[:1])
	}()
	index.Reset()
	xs[0] = .25
	ys[0] = -1
	index.Update(xs[1:], ys[1:])
	checkIndex(t, &index, xs[1:], ys[1:], 0, 1, 10)
}

func indexSignal(n int, kind string) ([]float32, []float32) {
	xs, ys := make([]float32, n), make([]float32, n)
	for i := range xs {
		xs[i] = float32(i)
		switch kind {
		case "noise":
			ys[i] = float32(uint32(i)*1664525+1013904223) / math.MaxUint32
		case "spikes":
			if i%997 == 0 {
				ys[i] = 1
			}
		case "constant":
			ys[i] = 1
		case "step":
			ys[i] = float32((i / 100) % 2)
		default:
			ys[i] = float32(math.Sin(float64(i) / 20))
		}
		if kind == "duplicate" {
			xs[i] = float32(i / 4)
		}
		if kind == "uneven" {
			xs[i] = float32(i) * float32(i) / float32(n)
		}
		if kind == "gapped" && i%997 == 0 {
			ys[i] = float32(math.NaN())
		}
	}
	return xs, ys
}

var indexSink float32

// Query timings include normalization for the stateless implementations.
func BenchmarkIndexQuery(b *testing.B) {
	for _, n := range []int{15000, 100000, 1000000} {
		for _, kind := range []string{"sine", "noise", "spikes", "constant", "step", "duplicate", "uneven", "gapped"} {
			for _, fraction := range []float32{1, .01} {
				name := fmt.Sprintf("%d/%s/visible%g", n, kind, fraction)
				xs, ys := indexSignal(n, kind)
				lo, hi := xs[n-1]*(1-fraction), xs[n-1]
				for _, mode := range []string{"MinMax", "Slice", "Index"} {
					b.Run(name+"/"+mode, func(b *testing.B) {
						var index MinMaxIndex
						index.Update(xs, ys)
						work := make([]float32, n)
						dx, dy := make([]float32, 0, n), make([]float32, 0, n)
						b.ReportAllocs()
						if mode == "Index" {
							dx, dy = index.Query(dx, dy, lo, hi, 1280)
						}
						for b.Loop() {
							if mode == "Index" {
								dx, dy = index.Query(dx, dy, lo, hi, 1280)
							} else {
								a, z := 0, n
								if mode == "Slice" {
									a = max(0, sort.Search(n, func(i int) bool { return xs[i] >= lo })-1)
									z = min(n, sort.Search(n, func(i int) bool { return xs[i] > hi })+1)
								}
								for i := a; i < z; i++ {
									work[i] = (xs[i] - lo) / (hi - lo)
								}
								dx, dy = MinMax(dx, dy, work[a:z], ys[a:z], 1280)
							}
							if len(dy) > 0 {
								indexSink = dy[len(dy)-1]
							}
						}
					})
				}
			}
		}
	}
}

func BenchmarkIndexBuild(b *testing.B) {
	xs, ys := indexSignal(1000000, "noise")
	b.ReportAllocs()
	for b.Loop() {
		var index MinMaxIndex
		index.Update(xs, ys)
		indexSink = ys[index.levels[0][0][0]]
		bytes := cap(index.runs)*16 + cap(index.work)*4
		for _, level := range index.levels {
			bytes += cap(level) * 16
		}
		b.ReportMetric(float64(bytes), "index-B")
	}
}

// BenchmarkIndexStreaming includes suffix updates, scrolling/zooming queries,
// and a bounded-history rebuild every 4096 samples. Build is not free.
func BenchmarkIndexStreaming(b *testing.B) {
	for _, n := range []int{15000, 100000, 1000000} {
		for _, width := range []int{1280, 1600, 2560, 3840} {
			for _, mode := range []string{"MinMax", "Index"} {
				b.Run(fmt.Sprintf("%d/%d/%s", n, width, mode), func(b *testing.B) {
					xs, ys := indexSignal(n+4096, "noise")
					work := make([]float32, len(xs))
					dx, dy := make([]float32, 0, n), make([]float32, 0, n)
					var index MinMaxIndex
					index.Update(xs[:n], ys[:n])
					dx, dy = index.Query(dx, dy, 0, float32(n-1), width)
					frame := 0
					b.ReportAllocs()
					for b.Loop() {
						frame = (frame + 1) % 256
						now := n + frame*16
						lo, hi := float32(frame*16), float32(now-1)
						if frame%32 >= 16 {
							lo += float32(n) / 2
						}
						if mode == "Index" {
							if frame == 0 {
								index.Reset()
							}
							index.Update(xs[:now], ys[:now])
							dx, dy = index.Query(dx, dy, lo, hi, width)
						} else {
							for i := 0; i < now; i++ {
								work[i] = (xs[i] - lo) / (hi - lo)
							}
							dx, dy = MinMax(dx, dy, work[:now], ys[:now], width)
						}
						indexSink = dy[len(dy)-1]
					}
				})
			}
		}
	}
}

func BenchmarkIndexAppend(b *testing.B) {
	xs, ys := indexSignal(1000000, "noise")
	var index MinMaxIndex
	index.Update(xs[:999984], ys[:999984])
	// Restore lengths outside the operation: this measures one completed block,
	// reusing allocated capacity, rather than an unbounded growing benchmark.
	b.ReportAllocs()
	for b.Loop() {
		index.xs = index.xs[:999984]
		index.ys = index.ys[:999984]
		index.runs[0][1] = 999984
		for l := range index.levels {
			index.levels[l] = index.levels[l][:999984/(minMaxBlock<<l)]
		}
		index.Update(xs, ys)
	}
}
