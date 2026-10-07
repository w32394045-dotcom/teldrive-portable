package telegramstore

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const (
	retryBaseDelay = 250 * time.Millisecond
	retryMaxDelay  = 5 * time.Second
)

var transientTelegramErrors = []string{
	"RPC_CALL_FAIL",
	"RPC_MCGET_FAIL",
	"WORKER_BUSY_TOO_LONG_RETRY",
	"STORAGE_CHOOSE_VOLUME_FAILED",
}

var transientTelegramMessages = []string{
	"timeout",
	"timedout",
	"no workers running",
	"memory limit exit",
	"connection dead",
	"engine was closed",
	"broken pipe",
	"connection reset by peer",
}

type retryMiddleware struct{ max int }

func (m retryMiddleware) Handle(next tg.Invoker) telegram.InvokeFunc {
	return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		for attempt := 0; ; attempt++ {
			err := next.Invoke(ctx, input, output)
			if err == nil {
				return nil
			}
			if attempt >= m.max || !isTransientTelegramError(err) {
				return err
			}
			// Retrying a server-busy error immediately burns the whole retry
			// budget in milliseconds and is itself a good way to earn a
			// FLOOD_WAIT, so back off with jitter between attempts and stop as
			// soon as the caller's context ends.
			if sleepErr := sleepWithContext(ctx, retryBackoff(attempt)); sleepErr != nil {
				return sleepErr
			}
		}
	}
}

// retryBackoff grows from retryBaseDelay to retryMaxDelay and returns a value in
// [delay/2, delay]. The jitter keeps parts retried in parallel from marching in
// lockstep and re-triggering the same congestion.
func retryBackoff(attempt int) time.Duration {
	delay := retryBaseDelay
	for i := 0; i < attempt && delay < retryMaxDelay; i++ {
		delay *= 2
	}
	if delay > retryMaxDelay {
		delay = retryMaxDelay
	}
	half := delay / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isTransientTelegramError(err error) bool {
	if tgerr.Is(err, transientTelegramErrors...) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, fragment := range transientTelegramMessages {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func newRetryMiddleware(max int) (telegram.Middleware, error) {
	if max < 0 {
		return nil, fmt.Errorf("Telegram retry count cannot be negative")
	}
	return retryMiddleware{max: max}, nil
}
