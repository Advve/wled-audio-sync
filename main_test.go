package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"math"
	"math/cmplx"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"testing"
)

func TestFFTMatchesDFT(t *testing.T) {
	const n = 64
	x := make([]complex128, n)
	for i := range x {
		x[i] = complex(rand.Float64()-0.5, 0)
	}
	want := make([]complex128, n)
	for k := range want {
		for j, v := range x {
			want[k] += v * cmplx.Exp(complex(0, -2*math.Pi*float64(k*j)/n))
		}
	}
	fft(x)
	for k := range x {
		if cmplx.Abs(x[k]-want[k]) > 1e-9 {
			t.Fatalf("bin %d: got %v want %v", k, x[k], want[k])
		}
	}
}

func sine(freq, amp float64, rate, n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(amp * math.Sin(2*math.Pi*freq*float64(i)/float64(rate)))
	}
	return out
}

func TestAnalyzerSine(t *testing.T) {
	for _, tc := range []struct {
		freq float64
		band int
	}{{60, 0}, {500, 5}, {1000, 7}, {5000, 14}} { // tones in the middle of a band

		a := NewAnalyzer(50)
		var f Frame
		for i := 0; i < 20; i++ { // let smoothing settle
			f = a.Process(sine(tc.freq, 0.3, 48000, fftSize), 48000, float64(i)*0.02)
		}
		if math.Abs(float64(f.MajorPeak)-tc.freq) > 15 {
			t.Errorf("%v Hz: major peak %v", tc.freq, f.MajorPeak)
		}
		loudest := 0
		for i, v := range f.Bands {
			if v > f.Bands[loudest] {
				loudest = i
			}
		}
		if loudest != tc.band {
			t.Errorf("%v Hz: loudest band %d, want %d (%v)", tc.freq, loudest, tc.band, f.Bands)
		}
		for i, v := range f.Bands { // per-band gain must not light up bands far from the tone
			if (i < tc.band-2 || i > tc.band+2) && v > 60 {
				t.Errorf("%v Hz: band %d = %v, want dark (%v)", tc.freq, i, v, f.Bands)
			}
		}
		if f.VolumeSmooth < 100 || f.Magnitude < 100 {
			t.Errorf("%v Hz: volume %v magnitude %v too low", tc.freq, f.VolumeSmooth, f.Magnitude)
		}
	}
}

func TestAnalyzerSilenceAndBeat(t *testing.T) {
	a := NewAnalyzer(50)
	f := a.Process(make([]float32, fftSize), 48000, 0)
	if !a.Silent || f.VolumeRaw != 0 || f.Bands != [16]float32{} {
		t.Fatalf("silence not detected: %+v", f)
	}
	quiet := sine(80, 0.02, 48000, fftSize)
	for i := 0; i < 50; i++ {
		a.Process(quiet, 48000, float64(i)*0.02)
	}
	f = a.Process(sine(80, 0.5, 48000, fftSize), 48000, 1.0)
	if !f.Beat {
		t.Fatal("bass jump not detected as beat")
	}
}

func TestPacketLayout(t *testing.T) {
	f := Frame{VolumeRaw: 12.5, VolumeSmooth: 99, Beat: true, Magnitude: 500, MajorPeak: 440}
	for i := range f.Bands {
		f.Bands[i] = float32(i * 16)
	}
	f.Bands[15] = 300 // clamps to 254
	b := encodePacket(f)
	if len(b) != 44 || string(b[:6]) != "00002\x00" {
		t.Fatalf("bad header/size: %q (%d)", b[:6], len(b))
	}
	if math.Float32frombits(binary.LittleEndian.Uint32(b[40:])) != 440 || b[16] != 1 || b[18+15] != 254 {
		t.Fatal("fields at wrong offsets")
	}
	g, ok := decodePacket(b)
	if !ok || g.MajorPeak != 440 || g.VolumeSmooth != 99 || g.Bands[3] != 48 {
		t.Fatalf("roundtrip: %+v", g)
	}
}

func TestTranslations(t *testing.T) {
	for key, tr := range texts {
		if strings.TrimSpace(tr[0]) == "" || strings.TrimSpace(tr[1]) == "" {
			t.Errorf("%q is missing a translation: %q", key, tr)
		}
	}
	defer setLanguage(langEN)
	setLanguage(langPL)
	if T("quit") != "Zakończ" || (Status{State: StatePaused}).Text() != "Wstrzymano" {
		t.Fatal("Polish not applied")
	}
	setLanguage(langEN)
	if T("quit") != "Quit" || T("no-such-key") != "no-such-key" {
		t.Fatal("English / missing-key fallback wrong")
	}
	for _, s := range sensitivity { // every menu key used in code exists
		if _, ok := texts[s.key]; !ok {
			t.Errorf("missing text %q", s.key)
		}
	}
}

func TestSingleInstance(t *testing.T) {
	name := "WLED-Audio-Sync-test-" + strconv.Itoa(rand.Int()) // not the real name: the app may be running
	release, ok := acquireSingleInstance(name)
	if !ok {
		t.Fatal("first instance refused")
	}
	if _, ok := acquireSingleInstance(name); ok {
		t.Fatal("second instance allowed while the first runs")
	}
	release()
	release2, ok := acquireSingleInstance(name)
	if !ok {
		t.Fatal("refused after the first instance exited")
	}
	release2()
}

func TestSubnetBroadcast(t *testing.T) {
	for _, tc := range []struct{ cidr, want string }{
		{"192.168.1.23/24", "192.168.1.255"},
		{"10.1.2.3/16", "10.1.255.255"},
		{"172.16.5.9/20", "172.16.15.255"},
	} {
		ip, n, _ := net.ParseCIDR(tc.cidr)
		n.IP = ip
		if got := subnetBroadcast(n).String(); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.cidr, got, tc.want)
		}
	}
}

func TestRingDecimates(t *testing.T) {
	var r Ring
	r.Push(sine(1000, 0.5, 96000, 8192), 96000)
	dst := make([]float32, fftSize)
	rate, age := r.Read(dst)
	if rate != 48000 || age > 1e9 {
		t.Fatalf("rate %d age %v", rate, age)
	}
}

func TestICO(t *testing.T) {
	b := encodeICO(StateActive, true, 16, 32)
	var hdr [3]uint16
	binary.Read(bytes.NewReader(b), binary.LittleEndian, &hdr)
	if hdr != [3]uint16{0, 1, 2} {
		t.Fatalf("header %v", hdr)
	}
	size := binary.LittleEndian.Uint32(b[6+16+8:])
	offset := binary.LittleEndian.Uint32(b[6+16+12:])
	if int(offset+size) != len(b) || size != 40+32*32*4+4*32 {
		t.Fatalf("entry 2: offset %d size %d total %d", offset, size, len(b))
	}
	img := renderIcon(32, StateActive, false)
	if img.Bounds() != image.Rect(0, 0, 32, 32) || img.NRGBAAt(16, 16).A == 0 || img.NRGBAAt(0, 0).A != 0 {
		t.Fatal("expected the middle bar at the centre and empty corners")
	}
	for _, size := range []int{16, 20, 24, 32, 40, 48, 64} { // glyph must fit at every tray size
		img := renderIcon(size, StateActive, true)
		for y := 0; y < size; y++ {
			if img.NRGBAAt(0, y).A != 0 && img.NRGBAAt(size-1, y).A != 0 {
				t.Fatalf("%d px: glyph touches both edges", size)
			}
		}
	}
}
