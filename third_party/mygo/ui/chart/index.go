package chart

import (
	"math"
	"math/bits"
	"sort"
)

// MinMaxIndex keeps extrema of completed blocks for repeated MinMax queries.
// The zero value is empty. Update borrows the input; keep indexed samples
// unchanged, and call Reset before replacing or dropping history. Do not
// copy a populated index or access it concurrently. Raw samples remain available to
// the caller for exact selections.
type MinMaxIndex struct {
	xs, ys []float32
	runs   [][2]int
	levels [][][2]int // level l holds extrema of completed 16<<l sample blocks
	work   []float32
}

const minMaxBlock = 16

// Below this density, scanning beats the hierarchy's boundary visits.
const minMaxScan = 4 * minMaxBlock

// Reset releases the borrowed data and all index storage.
func (s *MinMaxIndex) Reset() { *s = MinMaxIndex{} }

// Update indexes the appended suffix of xs and ys, up to their shorter length.
// Pass the entire retained history each time. X must be nondecreasing within
// each finite run, as for MinMax. Changing existing samples requires Reset.
func (s *MinMaxIndex) Update(xs, ys []float32) {
	n, old := min(len(xs), len(ys)), len(s.xs)
	if n < old {
		panic("chart: Reset MinMaxIndex before dropping samples")
	}
	s.xs, s.ys = xs[:n], ys[:n]
	for i := old; i < n; i++ {
		if !indexFinite(xs[i]) || !indexFinite(ys[i]) {
			continue
		}
		if len(s.runs) > 0 && s.runs[len(s.runs)-1][1] == i {
			s.runs[len(s.runs)-1][1]++
		} else {
			s.runs = append(s.runs, [2]int{i, i + 1})
		}
	}
	if len(s.levels) == 0 {
		s.levels = append(s.levels, nil)
	}
	for b := old / minMaxBlock; b < n/minMaxBlock; b++ {
		e := [2]int{b * minMaxBlock, b * minMaxBlock}
		for i := b*minMaxBlock + 1; i < (b+1)*minMaxBlock; i++ {
			e = s.merge(e, [2]int{i, i})
		}
		s.levels[0] = append(s.levels[0], e)
		for level := 0; len(s.levels[level])%2 == 0; level++ {
			v := s.levels[level]
			if level+1 == len(s.levels) {
				s.levels = append(s.levels, nil)
			}
			s.levels[level+1] = append(s.levels[level+1], s.merge(v[len(v)-2], v[len(v)-1]))
		}
	}
}

func indexFinite(v float32) bool { return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) }

func (s *MinMaxIndex) merge(a, b [2]int) [2]int {
	if s.ys[b[0]] < s.ys[a[0]] {
		a[0] = b[0]
	}
	if s.ys[b[1]] > s.ys[a[1]] {
		a[1] = b[1]
	}
	return a
}

// Query returns MinMax of the fractions (x-lo)/(hi-lo), without transforming
// the whole input. Y values are unchanged. lo and hi must be finite with
// lo < hi and a finite float32 difference; otherwise the output is empty.
// As with MinMax, output buffers must not alias input and are reused.
// Resize, pan and zoom need only another Query, not an index rebuild.
func (s *MinMaxIndex) Query(dstX, dstY []float32, lo, hi float32, pixelWidth int) ([]float32, []float32) {
	dstX, dstY = dstX[:0], dstY[:0]
	span := hi - lo
	if pixelWidth <= 0 || !indexFinite(lo) || !indexFinite(hi) || !indexFinite(span) || !(span > 0) {
		return dstX, dstY
	}
	x := func(i int) float32 { return (s.xs[i] - lo) / span }
	// With dense gaps there is little hierarchy to skip. One scan avoids
	// paying for a separate query per run when the whole history is visible.
	scan := len(s.xs)/minMaxScan <= pixelWidth || len(s.runs) > max(64, pixelWidth/minMaxBlock)
	if scan && len(s.xs) > 0 && x(0) >= 0 && x(len(s.xs)-1) <= 1 {
		return s.scan(dstX, dstY, 0, len(s.xs), lo, span, pixelWidth)
	}
	bucket := func(i int) float64 { return math.Floor(float64(x(i)) * float64(pixelWidth)) }
	for _, run := range s.runs {
		from, to := run[0], run[1]
		// Normalization can overflow even when the original X is finite.
		from += sort.Search(to-from, func(i int) bool { return x(from+i) > float32(math.Inf(-1)) })
		to = from + sort.Search(to-from, func(i int) bool { return x(from+i) >= float32(math.Inf(1)) })
		a := from + sort.Search(to-from, func(i int) bool { return x(from+i) >= 0 })
		if a == to {
			continue
		}
		b := from + sort.Search(to-from, func(i int) bool { return x(from+i) > 1 })
		if a == from && a == b {
			continue
		}
		if a > from {
			a--
		}
		if b < to {
			b++
		}
		if len(dstX) > 0 {
			dstX = append(dstX, float32(math.NaN()))
			dstY = append(dstY, float32(math.NaN()))
		}
		// At low density scanning is cheaper than visiting the hierarchy.
		if (b-a)/minMaxScan <= pixelWidth {
			n := len(dstX)
			xs, ys := s.scan(dstX[n:], dstY[n:], a, b, lo, span, pixelWidth)
			dstX = append(dstX[:n], xs...)
			dstY = append(dstY[:n], ys...)
			continue
		}
		last := -1
		emit := func(i int) {
			if i != last {
				dstX = append(dstX, x(i))
				dstY = append(dstY, s.ys[i])
				last = i
			}
		}
		emit(a)
		low, high, col := a, a, bucket(a)
		flush := func() { emit(min(low, high)); emit(max(low, high)) }
		add := func(i, j int) {
			c := bucket(i)
			if c != col || x(i) < 0 || x(i) > 1 {
				flush()
				low, high, col = i, i, c
			}
			if s.ys[i] < s.ys[low] {
				low = i
			}
			if s.ys[j] < s.ys[low] {
				low = j
			}
			if s.ys[i] > s.ys[high] {
				high = i
			}
			if s.ys[j] > s.ys[high] {
				high = j
			}
		}
		var visit func(start, level int)
		visit = func(start, level int) {
			end := start + (minMaxBlock << level)
			if x(start) >= 0 && x(end-1) <= 1 && bucket(start) == bucket(end-1) {
				e := s.levels[level][start/(minMaxBlock<<level)]
				// Feed in time order so equal extrema retain their first sample.
				add(min(e[0], e[1]), max(e[0], e[1]))
				return
			}
			if level == 0 {
				for i := start; i < end; i++ {
					add(i, i)
				}
				return
			}
			visit(start, level-1)
			visit(start+(minMaxBlock<<(level-1)), level-1)
		}
		for i := a; i < b; {
			if i%minMaxBlock != 0 || b-i < minMaxBlock {
				add(i, i)
				i++
				continue
			}
			block := i / minMaxBlock
			level := bits.Len(uint((b-i)/minMaxBlock)) - 1
			if block != 0 {
				level = min(level, bits.TrailingZeros(uint(block)))
			}
			visit(i, level)
			i += minMaxBlock << level
		}
		flush()
		emit(b - 1)
	}
	return dstX, dstY
}

func (s *MinMaxIndex) scan(dstX, dstY []float32, from, to int, lo, span float32, width int) ([]float32, []float32) {
	if cap(s.work) < to-from {
		s.work = make([]float32, to-from)
	}
	s.work = s.work[:to-from]
	for i := range s.work {
		s.work[i] = (s.xs[from+i] - lo) / span
	}
	return MinMax(dstX, dstY, s.work, s.ys[from:to], width)
}
