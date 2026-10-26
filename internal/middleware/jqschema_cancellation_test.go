package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyToolResponseFilter_ResourceExhaustionCancellation(t *testing.T) {
	const largeRange = `reduce range(1000000000) as $i (0; . + $i)`
	// Tail recursion keeps memory bounded while exercising an unending computation.
	const recursion = `def spin: spin; spin`

	tests := []struct {
		name        string
		filter      string
		timeout     time.Duration
		cancelAfter time.Duration
		wantErr     error
	}{
		{
			name:    "large range with caller deadline",
			filter:  largeRange,
			timeout: 20 * time.Millisecond,
			wantErr: context.DeadlineExceeded,
		},
		{
			name:    "recursion with caller deadline",
			filter:  recursion,
			timeout: 20 * time.Millisecond,
			wantErr: context.DeadlineExceeded,
		},
		{
			name:        "large range with caller cancellation",
			filter:      largeRange,
			cancelAfter: 20 * time.Millisecond,
			wantErr:     context.Canceled,
		},
		{
			name:        "recursion with caller cancellation",
			filter:      recursion,
			cancelAfter: 20 * time.Millisecond,
			wantErr:     context.Canceled,
		},
		{
			name:    "recursion with default timeout",
			filter:  recursion,
			wantErr: context.DeadlineExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, err := CompileToolResponseFilter(tt.filter)
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.timeout > 0 {
				var cancelTimeout context.CancelFunc
				ctx, cancelTimeout = context.WithTimeout(ctx, tt.timeout)
				defer cancelTimeout()
			}
			require.NoError(t, ctx.Err(), "execution must start with a live context")
			if tt.cancelAfter > 0 {
				timer := time.AfterFunc(tt.cancelAfter, cancel)
				defer timer.Stop()
			}

			type result struct {
				value any
				err   error
			}
			done := make(chan result, 1)
			go func() {
				value, err := applyToolResponseFilter(ctx, code, nil)
				done <- result{value: value, err: err}
			}()

			wait := 2 * time.Second
			if tt.timeout == 0 && tt.cancelAfter == 0 {
				wait += DefaultJqTimeout
			}
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case got := <-done:
				assert.Nil(t, got.value)
				assert.ErrorIs(t, got.err, tt.wantErr)
				assert.ErrorContains(t, got.err, "tool response filter execution failed")
				if tt.timeout == 0 && tt.cancelAfter == 0 {
					assert.NoError(t, ctx.Err(), "default timeout must not cancel the caller")
				} else {
					assert.ErrorIs(t, ctx.Err(), tt.wantErr)
				}
			case <-timer.C:
				t.Fatal("resource-exhausting filter did not stop promptly")
			}
		})
	}
}
