//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jfreymuth/pulse"
)

// PulseAudio / PipeWire capture: records the monitor of an output device (what it plays).

func newPulseClient() (*pulse.Client, error) {
	c, err := pulse.NewClient(pulse.ClientApplicationName("WLED Audio Sync"))
	if err != nil {
		return nil, fmt.Errorf("can't connect to PulseAudio/PipeWire: %w", err)
	}
	return c, nil
}

func listOutputDevices() ([]string, error) {
	c, err := newPulseClient()
	if err != nil {
		return nil, err
	}
	defer c.Close()
	sinks, err := c.ListSinks()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(sinks))
	for _, s := range sinks {
		names = append(names, s.Name())
	}
	return names, nil
}

func findOutput(c *pulse.Client, query string) (*pulse.Sink, error) {
	if query == "" {
		s, err := c.DefaultSink()
		if err != nil {
			return nil, fmt.Errorf("no default output device: %w", err)
		}
		return s, nil
	}
	sinks, err := c.ListSinks()
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	for _, s := range sinks {
		if strings.Contains(strings.ToLower(s.Name()), q) || s.ID() == query {
			return s, nil
		}
	}
	return nil, fmt.Errorf("no output device matching %q", query)
}

func captureLoop(ctx context.Context, device string, ring *Ring, opened func(name string)) error {
	c, err := newPulseClient()
	if err != nil {
		return err
	}
	defer c.Close()
	sink, err := findOutput(c, device)
	if err != nil {
		return err
	}

	rate := 48000
	rec, err := c.NewRecord(
		pulse.Float32Writer(func(buf []float32) (int, error) {
			ring.Push(buf, rate)
			return len(buf), nil
		}),
		pulse.RecordMonitor(sink),
		pulse.RecordMediaName("WLED audio sync"),
		pulse.RecordMono,
		pulse.RecordSampleRate(rate),
		pulse.RecordLatency(0.02),
	)
	if err != nil {
		return fmt.Errorf("start recording: %w", err)
	}
	defer rec.Close()
	rate = rec.SampleRate()
	rec.Start()
	defer rec.Stop()
	opened(sink.Name())

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		if err := rec.Error(); err != nil {
			return fmt.Errorf("recording stopped: %w", err)
		}
		if !rec.Running() {
			return errors.New("recording stopped")
		}
		if device == "" {
			if s, err := c.DefaultSink(); err == nil && s.ID() != sink.ID() {
				return nil // default output changed: reopen
			}
		}
	}
}
