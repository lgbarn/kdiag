package k8s

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"golang.org/x/term"
)

// ExecOpts holds parameters for executing a command inside a container.
type ExecOpts struct {
	Namespace     string
	PodName       string
	ContainerName string
	Command       []string
	Stdin         io.Reader
	Stdout        io.Writer
	Stderr        io.Writer
	TTY           bool
}

// AttachOpts holds parameters for attaching to a running container.
// Unlike ExecOpts, there is no Command — attach connects to the container's
// existing process.
type AttachOpts struct {
	Namespace     string
	PodName       string
	ContainerName string
	Stdin         io.Reader
	Stdout        io.Writer
	Stderr        io.Writer
	TTY           bool
}

// ExecInContainer executes a command inside the specified container using the
// Kubernetes exec subresource. It sets up a WebSocket executor with SPDY fallback
// and, when TTY=true, puts the local terminal into raw mode and monitors resize events.
func ExecInContainer(ctx context.Context, client *Client, opts ExecOpts) error {
	req := client.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(opts.PodName).
		Namespace(opts.Namespace).
		SubResource("exec")

	req.VersionedParams(&corev1.PodExecOptions{
		Container: opts.ContainerName,
		Command:   opts.Command,
		Stdin:     opts.Stdin != nil,
		Stdout:    opts.Stdout != nil,
		Stderr:    opts.Stderr != nil,
		TTY:       opts.TTY,
	}, scheme.ParameterCodec)

	wsExec, err := remotecommand.NewWebSocketExecutor(client.Config, "GET", req.URL().String())
	if err != nil {
		return fmt.Errorf("failed to create WebSocket executor: %w", err)
	}

	spdyExec, err := remotecommand.NewSPDYExecutor(client.Config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	exec, err := remotecommand.NewFallbackExecutor(wsExec, spdyExec, httpstream.IsUpgradeFailure)
	if err != nil {
		return fmt.Errorf("failed to create fallback executor: %w", err)
	}

	streamOpts := remotecommand.StreamOptions{
		Stdin:  opts.Stdin,
		Stdout: opts.Stdout,
		Stderr: opts.Stderr,
		Tty:    opts.TTY,
	}

	if opts.TTY {
		fd := int(os.Stdin.Fd())
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("failed to put terminal into raw mode: %w", err)
		}
		defer term.Restore(fd, oldState)

		tsq := newTerminalSizeQueue()
		tsq.monitor(ctx)
		streamOpts.TerminalSizeQueue = tsq
	}

	return exec.StreamWithContext(ctx, streamOpts)
}

// AttachToContainer attaches to the specified container's stdin/stdout/stderr streams.
// When TTY=true, stderr is suppressed (merged into stdout per Kubernetes attach behaviour)
// and the local terminal is placed into raw mode.
func AttachToContainer(ctx context.Context, client *Client, opts AttachOpts) error {
	stderr := opts.Stderr != nil
	if opts.TTY {
		// When TTY is true the server merges stderr into stdout.
		stderr = false
	}

	req := client.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(opts.PodName).
		Namespace(opts.Namespace).
		SubResource("attach")

	req.VersionedParams(&corev1.PodAttachOptions{
		Container: opts.ContainerName,
		Stdin:     opts.Stdin != nil,
		Stdout:    opts.Stdout != nil,
		Stderr:    stderr,
		TTY:       opts.TTY,
	}, scheme.ParameterCodec)

	wsExec, err := remotecommand.NewWebSocketExecutor(client.Config, "GET", req.URL().String())
	if err != nil {
		return fmt.Errorf("failed to create WebSocket executor: %w", err)
	}

	spdyExec, err := remotecommand.NewSPDYExecutor(client.Config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	exec, err := remotecommand.NewFallbackExecutor(wsExec, spdyExec, httpstream.IsUpgradeFailure)
	if err != nil {
		return fmt.Errorf("failed to create fallback executor: %w", err)
	}

	var errWriter io.Writer
	if !opts.TTY {
		errWriter = opts.Stderr
	}

	streamOpts := remotecommand.StreamOptions{
		Stdin:  opts.Stdin,
		Stdout: opts.Stdout,
		Stderr: errWriter,
		Tty:    opts.TTY,
	}

	if opts.TTY {
		fd := int(os.Stdin.Fd())
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("failed to put terminal into raw mode: %w", err)
		}
		defer term.Restore(fd, oldState)

		tsq := newTerminalSizeQueue()
		tsq.monitor(ctx)
		streamOpts.TerminalSizeQueue = tsq
	}

	return exec.StreamWithContext(ctx, streamOpts)
}

// terminalSizeQueue implements remotecommand.TerminalSizeQueue by listening for
// SIGWINCH signals and forwarding the current terminal dimensions.
type terminalSizeQueue struct {
	resize chan remotecommand.TerminalSize
}

func newTerminalSizeQueue() *terminalSizeQueue {
	return &terminalSizeQueue{
		resize: make(chan remotecommand.TerminalSize, 1),
	}
}

// Next returns the next terminal size or nil if the channel is closed.
func (t *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	size, ok := <-t.resize
	if !ok {
		return nil
	}
	return &size
}

// monitor starts a goroutine that listens for SIGWINCH and pushes the current
// terminal size to the resize channel. It stops when ctx is done.
func (t *terminalSizeQueue) monitor(ctx context.Context) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)

	// Send initial size immediately.
	if w, h, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
		select {
		case t.resize <- remotecommand.TerminalSize{Width: uint16(w), Height: uint16(h)}:
		default:
		}
	}

	go func() {
		defer signal.Stop(sigCh)
		defer close(t.resize)
		for {
			select {
			case <-ctx.Done():
				return
			case <-sigCh:
				w, h, err := term.GetSize(int(os.Stdin.Fd()))
				if err != nil {
					continue
				}
				select {
				case t.resize <- remotecommand.TerminalSize{Width: uint16(w), Height: uint16(h)}:
				default:
					// Drop resize event if consumer is not ready.
				}
			}
		}
	}()
}
