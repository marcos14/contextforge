// Package ratelimit implements Redis-backed token-bucket-ish counters per
// minute window. Granularities supported:
//   - per token
//   - per token+tool
//   - per source IP (global ceiling)
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter increments and checks per-minute counters in Redis.
type Limiter struct {
	rdb *redis.Client
}

func New(rdb *redis.Client) *Limiter { return &Limiter{rdb: rdb} }

// Allow returns true if (key) is below max for the current minute window.
// Increments the counter atomically. A max of 0 disables the limit.
func (l *Limiter) Allow(ctx context.Context, key string, max int) (bool, error) {
	if max <= 0 {
		return true, nil
	}
	bucket := time.Now().UTC().Format("200601021504") // YYYYMMDDHHMM
	full := fmt.Sprintf("rl:%s:%s", key, bucket)
	pipe := l.rdb.TxPipeline()
	incr := pipe.Incr(ctx, full)
	pipe.Expire(ctx, full, 70*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, err
	}
	return incr.Val() <= int64(max), nil
}
