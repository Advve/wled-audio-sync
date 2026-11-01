package main

import (
	"math"
	"sync"
	"time"
)

// Ring keeps the most recent fftSize mono samples. Capture goroutines push into it; the
// frame loop reads a snapshot. Very high sample rates (88.2 kHz+) are averaged down.
type Ring struct {
	mu     sync.Mutex
	buf    [fftSize]float32
	pos    int
	rate   int // effective rate after decimation
	inRate int
	factor int
	acc    float32
	accN   int
	last   time.Time
}

func (r *Ring) Push(mono []float32, rate int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rate != r.inRate {
		r.inRate, r.factor, r.acc, r.accN = rate, 1, 0, 0
		if rate > 60000 {
			r.factor = int(math.Round(float64(rate) / 48000))
		}
		r.rate = rate / r.factor
	}
	for _, s := range mono {
		r.acc += s
		r.accN++
		if r.accN == r.factor {
			r.buf[r.pos] = r.acc / float32(r.factor)
			r.pos = (r.pos + 1) % fftSize
			r.acc, r.accN = 0, 0
		}
	}
	if len(mono) > 0 {
		r.last = time.Now()
	}
}

// Read copies the samples in order into dst (len fftSize) and reports the sample rate and how
// long ago audio last arrived.
func (r *Ring) Read(dst []float32) (rate int, age time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := copy(dst, r.buf[r.pos:])
	copy(dst[n:], r.buf[:r.pos])
	if r.last.IsZero() {
		return r.rate, time.Hour
	}
	return r.rate, time.Since(r.last)
}
