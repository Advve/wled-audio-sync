package main

import (
	"encoding/binary"
	"math"
)

// WLED AudioReactive "audio sync" v2 packet (usermods/audioreactive/audio_reactive.cpp):
//
//	0  header[6] "00002\0"      18 fftResult[16] uint8
//	6  reserved[2]              34 reserved uint16
//	8  sampleRaw float32        36 FFT_Magnitude float32
//	12 sampleSmth float32       40 FFT_MajorPeak float32
//	16 samplePeak uint8         44 bytes total, packed, little-endian
//	17 reserved uint8
const (
	multicastGroup = "239.0.0.1"
	defaultPort    = 11988
	packetSize     = 44
)

var syncHeader = []byte{'0', '0', '0', '0', '2', 0}

// Frame holds the values WLED's audio-reactive effects use.
type Frame struct {
	VolumeRaw    float32 // 0..255
	VolumeSmooth float32 // 0..255
	Beat         bool
	Bands        [16]float32 // 0..254
	Magnitude    float32     // strength of the dominant frequency
	MajorPeak    float32     // dominant frequency, Hz
}

func silentFrame() Frame { return Frame{Magnitude: 0.001, MajorPeak: 1} }

func encodePacket(f Frame) []byte {
	b := make([]byte, packetSize)
	copy(b, syncHeader)
	le := binary.LittleEndian
	le.PutUint32(b[8:], math.Float32bits(f.VolumeRaw))
	le.PutUint32(b[12:], math.Float32bits(f.VolumeSmooth))
	if f.Beat {
		b[16] = 1
	}
	for i, v := range f.Bands {
		b[18+i] = uint8(math.Max(0, math.Min(254, float64(v))))
	}
	le.PutUint32(b[36:], math.Float32bits(f.Magnitude))
	le.PutUint32(b[40:], math.Float32bits(f.MajorPeak))
	return b
}

func decodePacket(b []byte) (Frame, bool) {
	var f Frame
	if len(b) != packetSize || string(b[:6]) != string(syncHeader) {
		return f, false
	}
	le := binary.LittleEndian
	f.VolumeRaw = math.Float32frombits(le.Uint32(b[8:]))
	f.VolumeSmooth = math.Float32frombits(le.Uint32(b[12:]))
	f.Beat = b[16] != 0
	for i := range f.Bands {
		f.Bands[i] = float32(b[18+i])
	}
	f.Magnitude = math.Float32frombits(le.Uint32(b[36:]))
	f.MajorPeak = math.Float32frombits(le.Uint32(b[40:]))
	return f, true
}
