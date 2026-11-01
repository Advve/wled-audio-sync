package main

import (
	"math"
	"math/cmplx"
)

const (
	fftSize      = 2048 // ~43 ms at 48 kHz
	maxFrequency = 11025.0
)

// WLED's 16 GEQ channels in Hz, with its pink-noise correction and top-band damping.
var (
	bandsHz = [16][2]float64{{43, 86}, {86, 129}, {129, 216}, {216, 301}, {301, 430}, {430, 560},
		{560, 818}, {818, 1120}, {1120, 1421}, {1421, 1895}, {1895, 2412},
		{2412, 3015}, {3015, 3704}, {3704, 4479}, {4479, 7106}, {7106, 9259}}
	bandWeight = [16]float64{1.70, 1.71, 1.73, 1.78, 1.68, 1.56, 1.55, 1.63,
		1.79, 1.62, 1.80, 2.06, 2.47, 3.35, 6.83 * 0.88, 9.55 * 0.70}
)

// Analyzer turns raw audio into the values WLED's audio-reactive effects expect. Ranges follow
// WLED's own sound simulator: volume and bands 0..255, MajorPeak in Hz, Magnitude up to ~1000
// (tiny when silent). Automatic gain control keeps the lights lively at any PC volume.
//
// PC audio is full-range, unlike the small microphones WLED is tuned for, so bass would swamp
// everything. Each band therefore gets its own automatic gain (bounded by the loudest band, so
// near-silent bands aren't blown up) and has part of its running average removed, so changes
// in mids and highs (vocals, synths, hi-hats) move the lights as much as the kick drum does.
type Analyzer struct {
	Gain, Squelch, BeatThreshold float64 // Squelch is linear (not dB)
	Silent                       bool

	rate, dt                             float64
	window                               []float64
	binHz                                float64
	bandBins                             [16][]int
	peakLo, peakHi                       int
	peakWeight                           []float64
	agcDecay, fall, smoothAlpha, bassAlp float64
	floorAlpha                           float64

	volRef, fftRef, volSmooth float64
	volAvg, peakRef           float64
	bandRef, bandFloor        [16]float64
	bands                     [16]float64
	bassAvg, lastBeat         float64
	buf                       []complex128
	spec                      []float64
}

const (
	bandRefShare = 0.5 // a band's gain never exceeds what makes 50 % of the loudest band full scale
	floorRemove  = 0.6 // share of a band's running average subtracted, for contrast
)

func NewAnalyzer(fps int) *Analyzer {
	a := &Analyzer{Gain: 1, Squelch: dbToLinear(-60), BeatThreshold: 1.5, Silent: true, dt: 1 / float64(fps)}
	a.window = make([]float64, fftSize)
	for i := range a.window { // Hann
		a.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(fftSize-1))
	}
	a.buf = make([]complex128, fftSize)
	a.spec = make([]float64, fftSize/2+1)
	a.agcDecay = math.Pow(0.85, a.dt) // gain recovers ~15 %/s after loud parts
	a.fall = math.Pow(0.02, a.dt)     // fade-out speed in silence
	a.smoothAlpha = 1 - math.Exp(-a.dt/0.08)
	a.bassAlp = 1 - math.Exp(-a.dt/0.5)
	a.floorAlpha = 1 - math.Exp(-a.dt/1.5)
	return a
}

func dbToLinear(db float64) float64 { return math.Pow(10, db/20) }

func (a *Analyzer) setRate(rate int) {
	if float64(rate) == a.rate || rate <= 0 {
		return
	}
	a.rate = float64(rate)
	a.binHz = a.rate / fftSize
	nyquistBin := fftSize / 2
	for i, b := range bandsHz {
		lo := int(math.Ceil(b[0] / a.binHz))
		hi := int(math.Ceil(b[1]/a.binHz)) - 1
		if hi < lo { // band narrower than one bin: use the nearest bin
			c := int(math.Round((b[0] + b[1]) / 2 / a.binHz))
			lo, hi = c, c
		}
		hi = min(hi, nyquistBin)
		a.bandBins[i] = a.bandBins[i][:0]
		for k := lo; k <= hi; k++ {
			a.bandBins[i] = append(a.bandBins[i], k)
		}
	}
	a.peakLo = int(math.Ceil(43 / a.binHz))
	a.peakHi = min(int(maxFrequency/a.binHz), nyquistBin)
	// Music is roughly pink (amplitude falls ~1/sqrt(f)), so the raw loudest bin is nearly always
	// bass. Weighting by sqrt(f) finds the frequency that stands out, e.g. a melody or voice.
	a.peakWeight = make([]float64, nyquistBin+1)
	for k := range a.peakWeight {
		a.peakWeight[k] = math.Sqrt(float64(k) * a.binHz / 100)
	}
}

func (a *Analyzer) floorRef() float64 { return math.Sqrt(a.Squelch) }

// SilentFrame fades everything out; used when there's no (or too quiet) audio.
func (a *Analyzer) SilentFrame() Frame {
	a.Silent = true
	a.volSmooth *= a.fall
	f := silentFrame()
	f.VolumeSmooth = float32(a.volSmooth)
	for i := range a.bands {
		a.bands[i] *= a.fall
		f.Bands[i] = float32(a.bands[i])
	}
	return f
}

// Process analyses the most recent fftSize samples. now is in seconds.
func (a *Analyzer) Process(window []float32, rate int, now float64) Frame {
	a.setRate(rate)
	frameLen := min(len(window), max(int(a.rate*a.dt), int(a.rate/100)))
	var sum float64
	for _, s := range window[len(window)-frameLen:] {
		sum += float64(s) * float64(s)
	}
	rms := math.Sqrt(sum / float64(frameLen))
	if rms < a.Squelch {
		return a.SilentFrame()
	}
	a.Silent = false
	var f Frame

	// Volume (0..255), relative to a slowly decaying peak follower, with part of the running
	// average removed: mastered music is nearly always loud, and without this volume-driven
	// effects (Gravcenter, Pixelwave, ...) would sit at the same size.
	a.volRef = math.Max(rms, math.Max(a.volRef*a.agcDecay, a.Squelch*4))
	volFloor := floorRemove * math.Min(a.volAvg, 0.9*a.volRef)
	a.volAvg += (rms - a.volAvg) * a.floorAlpha
	volumeRaw := math.Max(0, math.Min(255, 255*(rms-volFloor)/(a.volRef-volFloor)*a.Gain))
	a.volSmooth += (volumeRaw - a.volSmooth) * a.smoothAlpha
	f.VolumeRaw, f.VolumeSmooth = float32(volumeRaw), float32(a.volSmooth)

	// Spectrum -> 16 bands, square-root compressed like WLED's default scaling.
	for i, s := range window {
		a.buf[i] = complex(float64(s)*a.window[i], 0)
	}
	fft(a.buf)
	norm := float64(fftSize) / 4 // full-scale sine -> 1.0 with a Hann window
	for i := range a.spec {
		a.spec[i] = cmplx.Abs(a.buf[i]) / norm
	}
	var level [16]float64
	top := 0.0
	for i, bins := range a.bandBins {
		var s float64
		for _, k := range bins {
			s += a.spec[k]
		}
		level[i] = math.Sqrt(s / float64(len(bins)) * bandWeight[i])
		top = math.Max(top, level[i])
	}
	a.fftRef = math.Max(top, math.Max(a.fftRef*a.agcDecay, a.floorRef()))
	for i := range a.bands {
		// per-band gain: relative to the band's own recent peak, but never more than
		// bandRefShare of the loudest band allows
		a.bandRef[i] = math.Max(level[i], a.bandRef[i]*a.agcDecay)
		ref := math.Max(a.bandRef[i], math.Max(bandRefShare*a.fftRef, a.floorRef()))
		floor := floorRemove * math.Min(a.bandFloor[i], 0.9*ref)
		a.bandFloor[i] += (level[i] - a.bandFloor[i]) * a.floorAlpha
		target := math.Max(0, math.Min(254, 254*(level[i]-floor)/(ref-floor)*a.Gain))
		if target > a.bands[i] {
			a.bands[i] = 0.75*target + 0.25*a.bands[i]
		} else {
			a.bands[i] = 0.22*target + 0.78*a.bands[i]
		}
		f.Bands[i] = float32(a.bands[i])
	}

	// Beat: bass jumps well above its recent average (max one per 100 ms, like WLED).
	bass := (level[0] + level[1] + level[2]) / 3
	if bass > a.bassAvg*a.BeatThreshold && bass > 0.3*a.fftRef && now-a.lastBeat > 0.1 {
		f.Beat = true
		a.lastBeat = now
	}
	a.bassAvg += (bass - a.bassAvg) * a.bassAlp

	// Dominant frequency (pink-weighted, parabolic interpolation) and its strength relative to
	// its recent maximum.
	peak := a.peakLo
	for k := a.peakLo; k <= a.peakHi; k++ {
		if a.spec[k]*a.peakWeight[k] > a.spec[peak]*a.peakWeight[peak] {
			peak = k
		}
	}
	strength := a.spec[peak] * a.peakWeight[peak]
	a.peakRef = math.Max(strength, math.Max(a.peakRef*a.agcDecay, a.Squelch))
	freq := float64(peak) * a.binHz
	if peak > 0 && peak < len(a.spec)-1 {
		l, c, r := a.spec[peak-1], a.spec[peak], a.spec[peak+1]
		if d := l - 2*c + r; d != 0 {
			freq += 0.5 * (l - r) / d * a.binHz
		}
	}
	f.MajorPeak = float32(math.Max(1, math.Min(maxFrequency, freq)))
	f.Magnitude = float32(math.Max(1, math.Min(1000, 1000*math.Sqrt(strength/a.peakRef)*a.Gain)))
	return f
}
