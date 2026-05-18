//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// watchTerminalResize fires `onResize` whenever the controlling terminal is
// resized (SIGWINCH). The returned function detaches the handler.
func watchTerminalResize(onResize func()) func() {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-winch:
				onResize()
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(winch)
		close(done)
	}
}
