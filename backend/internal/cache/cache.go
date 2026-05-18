// Package cache wraps a Redis client for tool-result caching.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrMiss = errors.New("cache miss")

type Cache struct{ rdb *redis.Client }

func New(rdb *redis.Client) *Cache { return &Cache{rdb: rdb} }

// Key builds a deterministic cache key for a tool execution.
//
// Includes tool ID + version + a hash of params. If perToken is true, the
// token ID is also included so different tokens get separate slots.
func Key(toolID string, version int, params map[string]any, perToken bool, tokenID string) string {
	b, _ := json.Marshal(params)
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:])
	if perToken {
		return fmt.Sprintf("cache:tool:%s:v%d:%s:tok=%s", toolID, version, h, tokenID)
	}
	return fmt.Sprintf("cache:tool:%s:v%d:%s", toolID, version, h)
}

// Get fetches a cached value and unmarshals it into dst. Returns ErrMiss when
// not present.
func (c *Cache) Get(ctx context.Context, key string, dst any) error {
	b, err := c.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrMiss
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// Set stores v under key with the given TTL.
func (c *Cache) Set(ctx context.Context, key string, v any, ttl time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, key, b, ttl).Err()
}

// InvalidateTool removes all cached entries for a given tool id (any version).
func (c *Cache) InvalidateTool(ctx context.Context, toolID string) (int64, error) {
	pattern := fmt.Sprintf("cache:tool:%s:*", toolID)
	iter := c.rdb.Scan(ctx, 0, pattern, 200).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 0, nil
	}
	return c.rdb.Del(ctx, keys...).Result()
}

// Ping checks if Redis is reachable.
func (c *Cache) Ping(ctx context.Context) error {
	if c == nil || c.rdb == nil {
		return errors.New("cache not configured")
	}
	return c.rdb.Ping(ctx).Err()
}
