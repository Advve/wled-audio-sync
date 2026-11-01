//go:build !windows && !linux

package main

import (
	"context"
	"errors"
)

var errUnsupported = errors.New("audio capture is only supported on Windows and Linux")

func listOutputDevices() ([]string, error) { return nil, errUnsupported }

func captureLoop(context.Context, string, *Ring, func(string)) error { return errUnsupported }
