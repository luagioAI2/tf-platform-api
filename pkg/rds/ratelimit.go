package rds

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ============================ 分布式限流 ============================

// slidingWindowScript 滑动窗口限流（Lua 原子执行）。
//
// 实现：ZSET 存请求时间戳，每次请求：
//  1. 移除窗口外的成员
//  2. 统计窗口内数量
//  3. 未超限则加入并设置过期
//
// 相比固定窗口，滑动窗口不会在窗口边界出现 2 倍突发。
const slidingWindowScript = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]

redis.call('ZREMRANGEBYSCORE', key, 0, now - window)
local count = redis.call('ZCARD', key)
if count < limit then
	redis.call('ZADD', key, now, member)
	redis.call('PEXPIRE', key, window)
	return count + 1
end
return -1
`

// RateLimiter Redis 滑动窗口限流器（多实例共享）
type RateLimiter struct {
	client *redis.Client
}

// NewRateLimiter 创建限流器
func NewRateLimiter(client *redis.Client) *RateLimiter {
	if client == nil {
		client = GetClient()
	}
	return &RateLimiter{client: client}
}

// Allow 判断是否放行。
//
//	key    限流维度（如 "login:1.2.3.4"）
//	limit  窗口内最大请求数
//	window 窗口大小
//
// 返回 (是否放行, 当前计数, 错误)。Redis 不可用时**放行**（fail-open），
// 避免限流组件故障导致全站不可用。
func (r *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int64, error) {
	if r.client == nil {
		return true, 0, ErrRedisUnavailable
	}
	if limit <= 0 || window <= 0 {
		return true, 0, nil
	}

	nowMs := time.Now().UnixMilli()
	member := fmt.Sprintf("%d-%d", nowMs, time.Now().UnixNano())

	res, err := r.client.Eval(ctx, slidingWindowScript,
		[]string{"ratelimit:" + key},
		nowMs, window.Milliseconds(), limit, member,
	).Int64()
	if err != nil {
		// fail-open：限流组件故障不应阻断业务
		return true, 0, err
	}
	if res < 0 {
		return false, int64(limit), nil
	}
	return true, res, nil
}

// AllowN 批量限流（一次扣减 n 个配额），用于加权限流
func (r *RateLimiter) AllowN(ctx context.Context, key string, n, limit int, window time.Duration) (bool, error) {
	ok, _, err := r.Allow(ctx, key, limit-n+1, window)
	return ok, err
}

// Reset 重置某个维度的计数（如管理员解封）
func (r *RateLimiter) Reset(ctx context.Context, key string) error {
	if r.client == nil {
		return ErrRedisUnavailable
	}
	return r.client.Del(ctx, "ratelimit:"+key).Err()
}

// ============================ 幂等键 ============================

// Idempotent 幂等控制：首次调用返回 (true, nil)，重复调用返回 (false, nil)。
//
//	key 业务幂等键（如 "order:create:<request_id>"）
//	ttl 去重有效期
func Idempotent(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	c := GetClient()
	if c == nil {
		// Redis 不可用时放行，由 DB 唯一索引兜底
		return true, ErrRedisUnavailable
	}
	ok, err := c.SetNX(ctx, "idem:"+key, 1, ttl).Result()
	if err != nil {
		return true, err
	}
	return ok, nil
}

// IdempotentResult 幂等 + 缓存首次结果。
//
// 首次执行 fn 并把结果写入 Redis；重复请求直接返回缓存结果。
func IdempotentResult(ctx context.Context, key string, ttl time.Duration, fn func() (string, error)) (string, bool, error) {
	c := GetClient()
	if c == nil {
		v, err := fn()
		return v, false, err
	}
	cacheKey := "idem:result:" + key

	// 已存在结果直接返回
	if v, err := c.Get(ctx, cacheKey).Result(); err == nil {
		return v, true, nil
	}

	// 抢占执行权
	ok, err := c.SetNX(ctx, "idem:lock:"+key, 1, ttl).Result()
	if err != nil {
		v, e := fn()
		return v, false, e
	}
	if !ok {
		// 其他实例正在执行：短暂等待后读结果
		for i := 0; i < 20; i++ {
			time.Sleep(50 * time.Millisecond)
			if v, err := c.Get(ctx, cacheKey).Result(); err == nil {
				return v, true, nil
			}
		}
		return "", false, fmt.Errorf("idempotent request timeout")
	}

	defer c.Del(ctx, "idem:lock:"+key)
	v, err := fn()
	if err != nil {
		return "", false, err
	}
	_ = c.Set(ctx, cacheKey, v, ttl).Err()
	return v, false, nil
}
