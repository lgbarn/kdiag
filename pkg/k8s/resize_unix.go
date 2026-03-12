//go:build !windows

package k8s

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
	"k8s.io/client-go/tools/remotecommand"
)

// monitor starts a goroutine that listens for SIGWINCH and pushes the current
// terminal size to the resize channel. It stops when ctx is done.
func (t *terminalSizeQueue) monitor(ctx context.Context) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)

	t.sendInitialSize()

	fd := int(os.Stdin.Fd()) // #nosec G115 -- fd is a small non-negative int
	go func() {
		defer signal.Stop(sigCh)
		defer close(t.resize)
		for {
			select {
			case <-ctx.Done():
				return
			case <-sigCh:
				w, h, err := term.GetSize(fd)
				if err != nil {
					continue
				}
				select {
				case t.resize <- remotecommand.TerminalSize{Width: clampUint16(w), Height: clampUint16(h)}:
				default:
					// Drop resize event if consumer is not ready.
				}
			}
		}
	}()
}
