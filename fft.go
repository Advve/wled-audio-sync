package main

import (
	"math"
	"math/cmplx"
)

// fft is an in-place iterative radix-2 FFT. len(x) must be a power of two.
func fft(x []complex128) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		step := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		half := size / 2
		for start := 0; start < n; start += size {
			w := complex(1, 0)
			for k := 0; k < half; k++ {
				a, b := x[start+k], x[start+k+half]*w
				x[start+k], x[start+k+half] = a+b, a-b
				w *= step
			}
		}
	}
}
