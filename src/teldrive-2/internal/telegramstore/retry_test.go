package telegramstore

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tgerr"
)

func TestRetryBackoffGrowsAndIsCapped(t *testing.T) {
	t.Parallel()
	first := retryBackoff(0)
	if first < retryBaseDelay/2 || first > retryBaseDelay {
		t.Fatalf("retryBackoff(0) = %v, want within [%v, %v]", first, retryBaseDelay/2, retryBaseDelay)
	}
	// A large attempt count must stay capped rather than overflow the shift.
	last := retryBackoff(64)
	if last < retryMaxDelay/2 || last > retryMaxDelay {
		t.Fatalf("retryBackoff(64) = %v, want within [%v, %v]", last, retryMaxDelay/2, retryMaxDelay)
	}
}

func TestRetryMiddlewareStopsBackingOffWhenContextIsCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	middleware := retryMiddleware{max: 5}
	invoke := middleware.Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls.Add(1)
		// The caller gives up while the middleware is about to back off.
		cancel()
		return tgerr.New(-503, "Timeout")
	}))

	started := time.Now()
	if err := invoke(ctx, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("invoke() error = %v, want context.Canceled", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1: a cancelled context must stop retrying", got)
	}
	if elapsed := time.Since(started); elapsed > retryBaseDelay {
		t.Fatalf("invoke() took %v, want it to abort without waiting out the backoff", elapsed)
	}
}

func TestIsTransientTelegramErrorMatchesRPCTimeout(t *testing.T) {
	err := tgerr.New(-503, "Timeout")
	if !isTransientTelegramError(err) {
		t.Fatalf("isTransientTelegramError(%v) = false, want true", err)
	}
}

func TestRetryMiddlewareRetriesRPCTimeout(t *testing.T) {
	var calls atomic.Int32
	middleware := retryMiddleware{max: 2}
	invoke := middleware.Handle(telegram.InvokeFunc(func(_ context.Context, _ bin.Encoder, _ bin.Decoder) error {
		if calls.Add(1) == 1 {
			return tgerr.New(-503, "Timeout")
		}
		return nil
	}))

	if err := invoke(context.Background(), nil, nil); err != nil {
		t.Fatalf("invoke() error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}

func TestRetryMiddlewareRetriesConnectionResetByPeer(t *testing.T) {
	var calls atomic.Int32
	middleware := retryMiddleware{max: 2}
	invoke := middleware.Handle(telegram.InvokeFunc(func(_ context.Context, _ bin.Encoder, _ bin.Decoder) error {
		if calls.Add(1) == 1 {
			return errors.New("send: write: write padded intermediate: write tcp 10.89.0.13:33760->10.89.0.3:443: write: connection reset by peer")
		}
		return nil
	}))

	if err := invoke(context.Background(), nil, nil); err != nil {
		t.Fatalf("invoke() error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}
