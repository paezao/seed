package models

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"
)

type retrying struct {
	inner    Model
	attempts int
	base     time.Duration
}

// WithRetry retries transient provider failures with jittered exponential backoff.
func WithRetry(m Model, attempts int) Model {
	return &retrying{inner: m, attempts: attempts, base: 2 * time.Second}
}

func (r *retrying) Name() string { return r.inner.Name() }

func (r *retrying) Generate(ctx context.Context, req Request) (*Response, error) {
	var err error
	for i := 0; i < r.attempts; i++ {
		var resp *Response
		resp, err = r.inner.Generate(ctx, req)
		if err == nil {
			return resp, nil
		}
		if !Retryable(err) || i == r.attempts-1 {
			break
		}
		d := r.base * time.Duration(1<<i)
		d += time.Duration(rand.Int64N(int64(d) / 2))
		slog.Warn("model call failed; retrying", "model", r.inner.Name(), "attempt", i+1, "wait", d, "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d):
		}
	}
	return nil, err
}
