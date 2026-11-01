//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// WASAPI loopback capture: records what an output device is playing.

func comInit() (func(), error) {
	runtime.LockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		var oleErr *ole.OleError
		if !errors.As(err, &oleErr) || oleErr.Code() != 1 { // 1 = S_FALSE, already initialised
			runtime.UnlockOSThread()
			return nil, err
		}
	}
	return func() {
		ole.CoUninitialize()
		runtime.UnlockOSThread()
	}, nil
}

func newEnumerator() (*wca.IMMDeviceEnumerator, error) {
	var de *wca.IMMDeviceEnumerator
	err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &de)
	return de, err
}

func deviceName(mmd *wca.IMMDevice) string {
	var ps *wca.IPropertyStore
	if err := mmd.OpenPropertyStore(wca.STGM_READ, &ps); err != nil {
		return "unknown device"
	}
	defer ps.Release()
	var pv wca.PROPVARIANT
	if err := ps.GetValue(&wca.PKEY_Device_FriendlyName, &pv); err != nil {
		return "unknown device"
	}
	return pv.String()
}

func forEachOutput(de *wca.IMMDeviceEnumerator, fn func(mmd *wca.IMMDevice, name string) bool) error {
	var dc *wca.IMMDeviceCollection
	if err := de.EnumAudioEndpoints(wca.ERender, wca.DEVICE_STATE_ACTIVE, &dc); err != nil {
		return err
	}
	defer dc.Release()
	var n uint32
	if err := dc.GetCount(&n); err != nil {
		return err
	}
	for i := uint32(0); i < n; i++ {
		var mmd *wca.IMMDevice
		if dc.Item(i, &mmd) != nil {
			continue
		}
		keep := fn(mmd, deviceName(mmd))
		if !keep {
			mmd.Release()
		} else {
			return nil
		}
	}
	return nil
}

func listOutputDevices() ([]string, error) {
	done, err := comInit()
	if err != nil {
		return nil, err
	}
	defer done()
	de, err := newEnumerator()
	if err != nil {
		return nil, err
	}
	defer de.Release()
	var names []string
	err = forEachOutput(de, func(_ *wca.IMMDevice, name string) bool {
		names = append(names, name)
		return false
	})
	return names, err
}

// findOutput returns the default output ("") or the first one whose name contains query.
func findOutput(de *wca.IMMDeviceEnumerator, query string) (*wca.IMMDevice, error) {
	var found *wca.IMMDevice
	if query == "" {
		err := de.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &found)
		if err != nil {
			return nil, fmt.Errorf("no default output device: %w", err)
		}
		return found, nil
	}
	q := strings.ToLower(query)
	err := forEachOutput(de, func(mmd *wca.IMMDevice, name string) bool {
		if strings.Contains(strings.ToLower(name), q) {
			found = mmd
			return true
		}
		return false
	})
	if err == nil && found == nil {
		err = fmt.Errorf("no output device matching %q", query)
	}
	return found, err
}

func defaultOutputID(de *wca.IMMDeviceEnumerator) string {
	var mmd *wca.IMMDevice
	if de.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &mmd) != nil {
		return ""
	}
	defer mmd.Release()
	var id string
	_ = mmd.GetId(&id)
	return id
}

// sampleFormat reports whether the mix format is float, and its bits per sample.
func sampleFormat(wfx *wca.WAVEFORMATEX) (isFloat bool, bits int) {
	const formatFloat, formatExtensible = 3, 0xFFFE
	bits = int(wfx.WBitsPerSample)
	switch wfx.WFormatTag {
	case formatFloat:
		return true, bits
	case formatExtensible:
		if wfx.CbSize >= 22 { // SubFormat GUID starts at byte 24; its first field is the format tag
			sub := *(*uint32)(unsafe.Add(unsafe.Pointer(wfx), 24))
			return sub == formatFloat, bits
		}
		return bits == 32, bits
	}
	return false, bits
}

func mixdown(dst []float32, data *byte, frames uint32, channels int, isFloat bool, bits int, silent bool) []float32 {
	n := int(frames)
	if silent || data == nil {
		for i := 0; i < n; i++ {
			dst = append(dst, 0)
		}
		return dst
	}
	total := n * channels
	sample := func(i int) float32 { return 0 }
	switch {
	case isFloat && bits == 32:
		s := unsafe.Slice((*float32)(unsafe.Pointer(data)), total)
		sample = func(i int) float32 { return s[i] }
	case bits == 16:
		s := unsafe.Slice((*int16)(unsafe.Pointer(data)), total)
		sample = func(i int) float32 { return float32(s[i]) / 32768 }
	case bits == 24:
		s := unsafe.Slice(data, total*3)
		sample = func(i int) float32 {
			v := int32(s[3*i]) | int32(s[3*i+1])<<8 | int32(int8(s[3*i+2]))<<16
			return float32(v) / 8388608
		}
	case bits == 32:
		s := unsafe.Slice((*int32)(unsafe.Pointer(data)), total)
		sample = func(i int) float32 { return float32(s[i]) / 2147483648 }
	}
	for f := 0; f < n; f++ {
		var sum float32
		for c := 0; c < channels; c++ {
			sum += sample(f*channels + c)
		}
		dst = append(dst, sum/float32(channels))
	}
	return dst
}

// captureLoop records from an output device until ctx is done, an error occurs, or (when
// following the default output) the default output changes. opened is called with the
// device name once recording works.
func captureLoop(ctx context.Context, device string, ring *Ring, opened func(name string)) error {
	done, err := comInit()
	if err != nil {
		return err
	}
	defer done()
	de, err := newEnumerator()
	if err != nil {
		return fmt.Errorf("audio devices: %w", err)
	}
	defer de.Release()

	mmd, err := findOutput(de, device)
	if err != nil {
		return err
	}
	defer mmd.Release()
	followID := ""
	if device == "" {
		_ = mmd.GetId(&followID)
	}

	var ac *wca.IAudioClient
	if err := mmd.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &ac); err != nil {
		return fmt.Errorf("open audio device: %w", err)
	}
	defer ac.Release()
	var wfx *wca.WAVEFORMATEX
	if err := ac.GetMixFormat(&wfx); err != nil {
		return fmt.Errorf("audio format: %w", err)
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(wfx)))
	isFloat, bits := sampleFormat(wfx)
	channels, rate := int(wfx.NChannels), int(wfx.NSamplesPerSec)

	const bufferDuration = 200 * 10000 // 200 ms in 100 ns units
	if err := ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, wca.AUDCLNT_STREAMFLAGS_LOOPBACK,
		bufferDuration, 0, wfx, nil); err != nil {
		return fmt.Errorf("start loopback capture: %w", err)
	}
	var acc *wca.IAudioCaptureClient
	if err := ac.GetService(wca.IID_IAudioCaptureClient, &acc); err != nil {
		return fmt.Errorf("start loopback capture: %w", err)
	}
	defer acc.Release()
	if err := ac.Start(); err != nil {
		return fmt.Errorf("start loopback capture: %w", err)
	}
	defer ac.Stop()
	opened(deviceName(mmd))

	mono := make([]float32, 0, 4800)
	nextCheck := time.Now().Add(3 * time.Second)
	for ctx.Err() == nil {
		var packet uint32
		if err := acc.GetNextPacketSize(&packet); err != nil {
			return fmt.Errorf("audio device lost: %w", err)
		}
		for packet > 0 {
			var data *byte
			var frames, flags uint32
			var pos, qpc uint64
			if err := acc.GetBuffer(&data, &frames, &flags, &pos, &qpc); err != nil {
				return fmt.Errorf("audio device lost: %w", err)
			}
			mono = mixdown(mono[:0], data, frames, channels, isFloat, bits, flags&wca.AUDCLNT_BUFFERFLAGS_SILENT != 0)
			if err := acc.ReleaseBuffer(frames); err != nil {
				return fmt.Errorf("audio device lost: %w", err)
			}
			ring.Push(mono, rate)
			if err := acc.GetNextPacketSize(&packet); err != nil {
				return fmt.Errorf("audio device lost: %w", err)
			}
		}
		if followID != "" && time.Now().After(nextCheck) {
			nextCheck = time.Now().Add(3 * time.Second)
			if id := defaultOutputID(de); id != "" && id != followID {
				return nil // default output changed (e.g. headphones plugged in): reopen
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}
