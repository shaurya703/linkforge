// Package ratelimit implements a distributed token-bucket limiter backed by Redis.
//
// Why token bucket: it bounds the *sustained* rate (refill rate) while still
// allowing short *bursts* up to the bucket capacity — a good fit for a write
// endpoint where occasional spikes are fine but sustained abuse is not. A fixed
// window would either reject legitimate bursts or allow double-rate spikes across
// a window boundary; a sliding-window log is accurate but stores per-request data.
//
// Why Lua: the read-modify-write of the bucket must be atomic across many app
// replicas sharing one Redis. The whole refill-and-take runs server-side in a
// single round trip, so concurrent requests can't race on the token count.
package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// bucketScript refills the bucket based on elapsed time, then tries to take one
// token. Returns {allowed(0|1), retry_after_seconds}.
var bucketScript = redis.NewScript(`
local key       = KEYS[1]
local rate      = tonumber(ARGV[1])
local capacity  = tonumber(ARGV[2])
local now       = tonumber(ARGV[3])
local requested = tonumber(ARGV[4])

local data   = redis.call('HMGET', key, 'tokens', 'ts')
local tokens = tonumber(data[1])
local ts     = tonumber(data[2])
if tokens == nil then
  tokens = capacity
  ts = now
end

local delta = math.max(0, now - ts)
tokens = math.min(capacity, tokens + delta * rate)

local allowed = 0
if tokens >= requested then
  allowed = 1
  tokens = tokens - requested
end

redis.call('HSET', key, 'tokens', tokens, 'ts', now)
redis.call('EXPIRE', key, math.ceil(capacity / rate) + 1)

local retry = 0
if allowed == 0 then
  retry = (requested - tokens) / rate
end
return {allowed, tostring(retry)}
`)

// Limiter is a Redis-backed token-bucket limiter.
type Limiter struct {
	client   *redis.Client
	rate     float64 // tokens per second
	capacity int     // bucket size (max burst)
	now      func() time.Time
}

// New constructs a Limiter. rate is tokens/sec, burst is bucket capacity.
func New(client *redis.Client, rate float64, burst int) *Limiter {
	return &Limiter{client: client, rate: rate, capacity: burst, now: time.Now}
}

// Allow consumes one token for the given identity (e.g. client IP). It returns
// whether the request is permitted and, if not, how long to wait. A Redis error
// is returned to the caller, which should fail open (allow) to avoid an outage
// taking the whole endpoint down.
func (l *Limiter) Allow(ctx context.Context, identity string) (bool, time.Duration, error) {
	now := float64(l.now().UnixNano()) / 1e9
	res, err := bucketScript.Run(ctx, l.client,
		[]string{"lf:rl:" + identity},
		l.rate, l.capacity, now, 1,
	).Result()
	if err != nil {
		return false, 0, err
	}
	vals, ok := res.([]interface{})
	if !ok || len(vals) != 2 {
		return false, 0, fmt.Errorf("unexpected script result: %v", res)
	}
	allowed, _ := vals[0].(int64)
	retrySecs, _ := strconv.ParseFloat(fmt.Sprint(vals[1]), 64)
	return allowed == 1, time.Duration(retrySecs * float64(time.Second)), nil
}
