package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"
	"sync"
)

// The tray icon is drawn in code: a sound-wave glyph of five rounded bars. Like modern tray icons
// it is monochrome (white on dark taskbars, near-black on light ones) and only uses colour to
// show activity: gradient = sending music, plain = idle, dimmed = paused, red badge = problem.
// (Paused keeps the full glyph: flat low bars shrank to a few faint pixels at tray size and
// looked like an empty slot.)

var (
	waveHeights = [5]float64{0.38, 0.72, 1.0, 0.62, 0.30}                                      // share of the full height, per bar
	gradient    = []color.NRGBA{{34, 211, 238, 255}, {168, 85, 247, 255}, {236, 72, 153, 255}} // cyan, violet, pink
	badgeRed    = color.NRGBA{239, 68, 68, 255}
)

func monoColor(dark bool) color.NRGBA {
	if dark {
		return color.NRGBA{245, 245, 245, 255}
	}
	return color.NRGBA{32, 32, 32, 255}
}

// gradientAt interpolates the activity gradient, t in 0..1.
func gradientAt(t float64) color.NRGBA {
	seg := t * float64(len(gradient)-1)
	i := min(int(seg), len(gradient)-2)
	f := seg - float64(i)
	a, b := gradient[i], gradient[i+1]
	mix := func(x, y uint8) uint8 { return uint8(float64(x)*(1-f) + float64(y)*f + 0.5) }
	return color.NRGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), 255}
}

// capsuleCoverage is the anti-aliased coverage of pixel (px,py) by a vertical capsule centred
// on x=cx from y0 to y1 (the straight part) with radius r.
func capsuleCoverage(px, py, cx, y0, y1, r float64) float64 {
	dy := math.Max(y0-py, math.Max(0, py-y1))
	return clamp01(r - math.Hypot(px-cx, dy) + 0.5)
}

func renderIcon(size int, state State, dark bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	sz := float64(size)
	// Whole-pixel bar widths and gaps keep the glyph crisp at 16-24 px.
	barW, gap := math.Max(2, math.Round(sz*0.125)), math.Max(1, math.Round(sz*0.0625))
	total := 5*barW + 4*gap
	left, r := math.Floor((sz-total)/2), barW/2
	maxH, cy := sz*0.84, sz/2
	alpha := 1.0
	if state == StatePaused {
		alpha = 0.45
	}
	// Problem badge: a red dot in the bottom-right corner, with a transparent ring cut around it.
	badgeX, badgeY, badgeR := sz*0.82, sz*0.8, sz*0.16
	gapR := badgeR + math.Max(1, sz*0.07)

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			var col color.NRGBA
			cov := 0.0
			for i, h := range waveHeights {
				cx := left + float64(i)*(barW+gap) + r
				half := math.Max(h*maxH/2, r)
				if c := capsuleCoverage(px, py, cx, cy-half+r, cy+half-r, r); c > cov {
					cov = c
					if state == StateActive {
						col = gradientAt(float64(i) / 4)
					} else {
						col = monoColor(dark)
					}
				}
			}
			cov *= alpha
			if state == StateError {
				d := math.Hypot(px-badgeX, py-badgeY)
				cov *= 1 - clamp01(gapR-d+0.5) // cut-out ring
				if b := clamp01(badgeR - d + 0.5); b > 0 {
					col, cov = badgeRed, math.Max(cov, b)
				}
			}
			if cov > 0 {
				col.A = uint8(cov*255 + 0.5)
				img.SetNRGBA(x, y, col)
			}
		}
	}
	return img
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// encodeICO builds a Windows .ico with 32-bit BMP entries at several sizes.
func encodeICO(state State, dark bool, sizes ...int) []byte {
	var images [][]byte
	for _, size := range sizes {
		img := renderIcon(size, state, dark)
		var b bytes.Buffer
		maskRow := ((size + 31) / 32) * 4
		hdr := []uint32{40, uint32(size), uint32(size * 2)} // BITMAPINFOHEADER: size, width, height*2
		for _, v := range hdr {
			binary.Write(&b, binary.LittleEndian, v)
		}
		binary.Write(&b, binary.LittleEndian, uint16(1))  // planes
		binary.Write(&b, binary.LittleEndian, uint16(32)) // bits per pixel
		binary.Write(&b, binary.LittleEndian, uint32(0))  // BI_RGB
		binary.Write(&b, binary.LittleEndian, uint32(size*size*4+maskRow*size))
		b.Write(make([]byte, 16))        // resolution, colours used/important
		for y := size - 1; y >= 0; y-- { // bottom-up BGRA
			for x := 0; x < size; x++ {
				p := img.NRGBAAt(x, y)
				b.Write([]byte{p.B, p.G, p.R, p.A})
			}
		}
		b.Write(make([]byte, maskRow*size)) // AND mask unused (alpha channel wins)
		images = append(images, b.Bytes())
	}
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, []uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, size := range sizes {
		dim := uint8(size)
		if size >= 256 {
			dim = 0
		}
		out.Write([]byte{dim, dim, 0, 0})
		binary.Write(&out, binary.LittleEndian, []uint16{1, 32})
		binary.Write(&out, binary.LittleEndian, []uint32{uint32(len(images[i])), uint32(offset)})
		offset += len(images[i])
	}
	for _, img := range images {
		out.Write(img)
	}
	return out.Bytes()
}

type iconKey struct {
	state State
	dark  bool
}

var (
	iconMu    sync.Mutex
	iconCache = map[iconKey][]byte{}
)

// trayIcon returns icon bytes in the format the tray expects on this OS.
func trayIcon(state State, dark bool) []byte {
	iconMu.Lock()
	defer iconMu.Unlock()
	key := iconKey{state, dark}
	if b, ok := iconCache[key]; ok {
		return b
	}
	var b []byte
	if runtime.GOOS == "windows" {
		b = encodeICO(state, dark, 16, 20, 24, 32, 40, 48, 64)
	} else {
		var buf bytes.Buffer
		_ = png.Encode(&buf, renderIcon(64, state, dark))
		b = buf.Bytes()
	}
	iconCache[key] = b
	return b
}
