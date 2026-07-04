package cmd

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeAndWait_GracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	httpServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveAndWait(
			ctx,
			cancel,
			httpServer,
			500*time.Millisecond,
			nil,
			func() error {
				return httpServer.Serve(listener)
			},
		)
	}()

	require.Eventually(t, func() bool {
		client := &http.Client{Timeout: 100 * time.Millisecond}
		resp, reqErr := client.Get("http://" + listener.Addr().String())
		if reqErr != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, time.Second, 20*time.Millisecond)

	cancel()
	require.NoError(t, <-errCh)
}

// TestServeAndWait_OnShutdownSignalCalled verifies that the optional
// onShutdownSignal callback is invoked when the context is cancelled.
func TestServeAndWait_OnShutdownSignalCalled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	httpServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}

	var signalCalled bool
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveAndWait(
			ctx,
			cancel,
			httpServer,
			500*time.Millisecond,
			func() { signalCalled = true },
			func() error {
				return httpServer.Serve(listener)
			},
		)
	}()

	require.Eventually(t, func() bool {
		client := &http.Client{Timeout: 100 * time.Millisecond}
		resp, reqErr := client.Get("http://" + listener.Addr().String())
		if reqErr != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, time.Second, 20*time.Millisecond)

	cancel()
	require.NoError(t, <-errCh)
	assert.True(t, signalCalled, "onShutdownSignal should have been called on shutdown")
}

// TestServeAndWait_ServeFnError verifies that when serveFn returns an unexpected
// error (not http.ErrServerClosed), serveAndWait triggers context cancellation
// and propagates the error to the caller.
func TestServeAndWait_ServeFnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	httpServer := &http.Server{}
	serveErrExpected := errors.New("unexpected serve error")

	result := serveAndWait(
		ctx,
		cancel,
		httpServer,
		500*time.Millisecond,
		nil,
		func() error {
			return serveErrExpected
		},
	)

	require.Error(t, result)
	assert.ErrorIs(t, result, serveErrExpected, "unexpected serve error should be propagated")
	assert.ErrorIs(t, ctx.Err(), context.Canceled, "serveAndWait should cancel the context on unexpected serve error")
}

// TestServeAndWait_ShutdownTimesOut verifies that when graceful shutdown exceeds
// its timeout because active connections are not drained in time, serveAndWait
// forces a close and returns the shutdown deadline error.
func TestServeAndWait_ShutdownTimesOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	handlerStarted := make(chan struct{})
	handlerUnblock := make(chan struct{})
	// Always unblock the handler goroutine when the test ends to prevent leaks.
	t.Cleanup(func() { close(handlerUnblock) })

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	httpServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(handlerStarted)
			<-handlerUnblock
			w.WriteHeader(http.StatusOK)
		}),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveAndWait(
			ctx,
			cancel,
			httpServer,
			time.Nanosecond, // near-zero timeout forces immediate shutdown deadline
			nil,
			func() error {
				return httpServer.Serve(listener)
			},
		)
	}()

	// Fire a request so the blocking handler holds an active connection open.
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		_, _ = client.Get("http://" + listener.Addr().String())
	}()

	// Wait until the handler is executing to guarantee an in-flight connection exists.
	select {
	case <-handlerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for handler to start")
	}

	// Trigger shutdown. With a 1 ns timeout the shutdown context expires immediately,
	// so httpServer.Shutdown returns context.DeadlineExceeded while the connection
	// is still open, exercising the forced-close path.
	cancel()

	result := <-errCh
	require.Error(t, result)
	assert.ErrorIs(t, result, context.DeadlineExceeded, "shutdown timeout should return context.DeadlineExceeded")
}
