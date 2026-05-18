//go:build windows

package main

// watchTerminalResize is a no-op on Windows: there is no SIGWINCH. Resize
// events for the spike are not delivered to the remote side on this OS.
func watchTerminalResize(onResize func()) func() {
	return func() {}
}
