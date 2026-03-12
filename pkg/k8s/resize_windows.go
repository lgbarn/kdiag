//go:build windows

package k8s

import "context"

// monitor sends the initial terminal size, then closes the channel.
// Windows does not have SIGWINCH; terminal resize is not tracked.
func (t *terminalSizeQueue) monitor(ctx context.Context) {
	t.sendInitialSize()

	go func() {
		<-ctx.Done()
		close(t.resize)
	}()
}
