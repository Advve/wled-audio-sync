package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

type State int

const (
	StateStarting State = iota
	StateActive         // sending music
	StateIdle           // silent, not sending
	StatePaused
	StateError
)

// idleAfter: seconds of silence (fade-out) before sending stops, so several PCs can run this
// and whichever one is playing drives the lights. WLED times out to silence by itself.
const idleAfter = time.Second

type Status struct {
	State  State
	Device string // capture device in use
	Target string // where packets go
	Err    string
}

func (s Status) Text() string {
	switch s.State {
	case StateActive:
		return T("status.active")
	case StateIdle:
		return T("status.idle")
	case StatePaused:
		return T("status.paused")
	case StateError:
		return T("status.problem") + s.Err
	}
	return T("status.starting")
}

// Engine captures audio, analyses it and sends audio sync packets at a fixed rate.
type Engine struct {
	OnStatus func(Status)       // called when the status changes
	OnFrame  func(Frame, State) // called every frame (optional; console meter)

	mu            sync.Mutex
	settings      Settings
	paused        bool
	senderDirty   bool
	cancelCapture context.CancelFunc
	device        string
	captureErr    string

	ring Ring
}

func NewEngine(s Settings) *Engine { return &Engine{settings: s, senderDirty: true} }

// sendFromIP picks the local address to broadcast from: --interface if set, else the adapter of
// the internet connection.
func sendFromIP(s Settings) string {
	if s.Interface != "" {
		return s.Interface
	}
	return guessLANIP()
}

func (e *Engine) Settings() Settings {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings
}

// Update changes settings; capture and network are restarted only if needed.
func (e *Engine) Update(change func(*Settings)) {
	e.mu.Lock()
	old := e.settings
	change(&e.settings)
	s := e.settings
	if s.Port != old.Port || s.Interface != old.Interface || s.Multicast != old.Multicast ||
		s.NoBroadcast != old.NoBroadcast || fmt.Sprint(s.Unicast) != fmt.Sprint(old.Unicast) {
		e.senderDirty = true
	}
	cancel := e.cancelCapture
	e.mu.Unlock()
	if s.Device != old.Device && cancel != nil {
		cancel()
	}
}

func (e *Engine) SetPaused(p bool) {
	e.mu.Lock()
	e.paused = p
	e.mu.Unlock()
}

func (e *Engine) Run(ctx context.Context) {
	go e.captureSupervisor(ctx)
	e.frameLoop(ctx)
}

func (e *Engine) captureSupervisor(ctx context.Context) {
	for ctx.Err() == nil {
		cctx, cancel := context.WithCancel(ctx)
		e.mu.Lock()
		e.cancelCapture = cancel
		device := e.settings.Device
		e.mu.Unlock()

		err := captureLoop(cctx, device, &e.ring, func(name string) {
			e.mu.Lock()
			e.device, e.captureErr = name, ""
			e.mu.Unlock()
			log.Printf("capturing: %s", name)
		})
		restarted := cctx.Err() != nil // device changed from the menu
		cancel()
		switch {
		case ctx.Err() != nil:
			return
		case err != nil && !restarted:
			log.Printf("audio: %v", err)
			e.mu.Lock()
			e.captureErr = err.Error()
			e.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		default:
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func (e *Engine) frameLoop(ctx context.Context) {
	s := e.Settings()
	an := NewAnalyzer(s.FPS)
	window := make([]float32, fftSize)
	var sender *Sender
	defer func() {
		if sender != nil {
			sender.Close()
		}
	}()
	start := time.Now()
	lastSound := time.Time{}
	nextNetCheck := time.Now().Add(10 * time.Second)
	var last Status
	tick := time.NewTicker(time.Second / time.Duration(s.FPS))
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		e.mu.Lock()
		s, paused, dirty := e.settings, e.paused, e.senderDirty
		e.senderDirty = false
		status := Status{Device: e.device, Err: e.captureErr}
		e.mu.Unlock()

		// The PC's network may change (laptop moves from cable to WiFi, new DHCP address):
		// re-work out the address to send from.
		if sender != nil && time.Now().After(nextNetCheck) {
			nextNetCheck = time.Now().Add(10 * time.Second)
			if ip := sendFromIP(s); ip != "" && ip != sender.LANIP {
				dirty = true
			}
		}
		if dirty || sender == nil {
			if sender != nil {
				sender.Close()
				sender = nil
			}
			var err error
			d := Delivery{Broadcast: !s.NoBroadcast, Multicast: s.Multicast, Unicast: s.Unicast}
			if sender, err = NewSender(s.Port, sendFromIP(s), d); err != nil {
				status.Err = "network: " + err.Error()
				e.mu.Lock()
				e.senderDirty = true // retry next frame
				e.mu.Unlock()
			} else {
				log.Printf("sending to %s via %s", sender.Desc, sender.LANIP)
			}
		}

		an.Gain, an.Squelch, an.BeatThreshold = s.Gain, dbToLinear(s.SquelchDB), s.Beat
		rate, age := e.ring.Read(window)
		var f Frame
		now := time.Now()
		if age > 250*time.Millisecond { // WASAPI loopback delivers nothing while the PC is silent
			f = an.SilentFrame()
		} else {
			f = an.Process(window, rate, now.Sub(start).Seconds())
		}
		if !an.Silent {
			lastSound = now
		}
		idle := now.Sub(lastSound) > idleAfter

		if sender != nil {
			status.Target = sender.Desc
			if !paused && !idle {
				if err := sender.Send(encodePacket(f)); err != nil {
					status.Err = "network: " + err.Error()
				}
			}
		}
		switch {
		case paused:
			status.State = StatePaused
		case status.Err != "":
			status.State = StateError
		case idle:
			status.State = StateIdle
		default:
			status.State = StateActive
		}
		if e.OnFrame != nil {
			e.OnFrame(f, status.State)
		}
		if status != last {
			if status.Err != last.Err && status.Err != "" {
				log.Print(status.Err)
			}
			last = status
			if e.OnStatus != nil {
				e.OnStatus(status)
			}
		}
	}
}
