package rds

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// 锁相关错误（调用方可据此判断是"竞争失败"还是"Redis 故障"）
var (
	ErrLockNotAcquired  = errors.New("lock not acquired")
	ErrLockNotOwned     = errors.New("lock not owned by this instance or already expired")
	ErrRedisUnavailable = errors.New("redis client is nil")
)

// unlockScript 只有 value 匹配才删除，保证不会误删他人持有的锁
const unlockScript = `
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
else
	return 0
end
`

// extendScript 只有 value 匹配才续期
const extendScript = `
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("pexpire", KEYS[1], ARGV[2])
else
	return 0
end
`

// Client 全局 Redis 客户端（由 providers 注入一次）。
// 用 RWMutex 保护，避免初始化与读取之间的 data race。
var (
	clientMu sync.RWMutex
	client   *redis.Client
)

// SetClient 由 providers 在建立连接后调用
func SetClient(c *redis.Client) {
	clientMu.Lock()
	client = c
	clientMu.Unlock()
}

// GetClient 返回全局 Redis 客户端（可能为 nil，调用方需判空）
func GetClient() *redis.Client {
	clientMu.RLock()
	defer clientMu.RUnlock()
	return client
}

// InitRDS 建立连接并注册为全局客户端
func InitRDS(opt *redis.Options) (*redis.Client, error) {
	c := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	SetClient(c)
	return c, nil
}

// DistributedLock Redis 分布式锁
type DistributedLock struct {
	client *redis.Client
	key    string
	value  string
	ttl    time.Duration
}

// NewDistributedLock 创建分布式锁（client 取自全局）
func NewDistributedLock(key string, ttl time.Duration) *DistributedLock {
	return NewDistributedLockWithClient(GetClient(), key, ttl)
}

// NewDistributedLockWithClient 显式指定 client（便于测试与多实例场景）
func NewDistributedLockWithClient(client *redis.Client, key string, ttl time.Duration) *DistributedLock {
	if ttl <= 0 {
		ttl = 10 * time.Second
	}
	return &DistributedLock{
		client: client,
		key:    "lock:" + key,
		value:  generateRandomValue(),
		ttl:    ttl,
	}
}

// Lock 获取锁，失败返回 ErrLockNotAcquired
func (l *DistributedLock) Lock(ctx context.Context) error {
	if l.client == nil {
		return ErrRedisUnavailable
	}
	ok, err := l.client.SetNX(ctx, l.key, l.value, l.ttl).Result()
	if err != nil {
		return fmt.Errorf("redis lock error: %w", err)
	}
	if !ok {
		return ErrLockNotAcquired
	}
	return nil
}

// TryLock 带重试的获取锁（指数退避，最大间隔 200ms）
func (l *DistributedLock) TryLock(ctx context.Context, maxRetries int, retryInterval time.Duration) error {
	if retryInterval <= 0 {
		retryInterval = 20 * time.Millisecond
	}
	interval := retryInterval

	for i := 0; i <= maxRetries; i++ {
		err := l.Lock(ctx)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrLockNotAcquired) {
			return err // Redis 故障，直接失败
		}
		if i == maxRetries {
			return ErrLockNotAcquired
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		// 指数退避，上限 200ms，避免大量重试打爆 Redis
		if interval < 200*time.Millisecond {
			interval *= 2
		}
	}
	return ErrLockNotAcquired
}

// Unlock 释放锁（Lua 保证原子性，只删自己持有的锁）
func (l *DistributedLock) Unlock(ctx context.Context) error {
	if l.client == nil {
		return ErrRedisUnavailable
	}
	res, err := l.client.Eval(ctx, unlockScript, []string{l.key}, l.value).Int64()
	if err != nil {
		return fmt.Errorf("redis unlock error: %w", err)
	}
	if res == 0 {
		return ErrLockNotOwned
	}
	return nil
}

// Extend 延长锁的过期时间
func (l *DistributedLock) Extend(ctx context.Context, ttl time.Duration) error {
	if l.client == nil {
		return ErrRedisUnavailable
	}
	res, err := l.client.Eval(ctx, extendScript, []string{l.key}, l.value, ttl.Milliseconds()).Int64()
	if err != nil {
		return fmt.Errorf("redis extend lock error: %w", err)
	}
	if res == 0 {
		return ErrLockNotOwned
	}
	return nil
}

// IsLocked 检查锁是否存在
func (l *DistributedLock) IsLocked(ctx context.Context) (bool, error) {
	if l.client == nil {
		return false, ErrRedisUnavailable
	}
	n, err := l.client.Exists(ctx, l.key).Result()
	if err != nil {
		return false, fmt.Errorf("redis check lock error: %w", err)
	}
	return n > 0, nil
}

// generateRandomValue 生成 16 字节随机值
func generateRandomValue() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// WithLock 获取锁 → 执行 → 释放。获取失败直接返回 ErrLockNotAcquired。
func WithLock(ctx context.Context, key string, ttl time.Duration, fn func() error) error {
	return WithLockClient(ctx, GetClient(), key, ttl, fn)
}

// WithLockClient 显式指定 client 的 WithLock
func WithLockClient(ctx context.Context, client *redis.Client, key string, ttl time.Duration, fn func() error) error {
	lock := NewDistributedLockWithClient(client, key, ttl)
	if err := lock.Lock(ctx); err != nil {
		return err
	}
	defer func() {
		// 释放用独立 ctx，避免业务 ctx 已取消导致锁无法释放
		unlockCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = lock.Unlock(unlockCtx)
	}()
	return fn()
}

// WithTryLock 带重试的 WithLock
func WithTryLock(ctx context.Context, key string, ttl time.Duration, maxRetries int, retryInterval time.Duration, fn func() error) error {
	lock := NewDistributedLock(key, ttl)
	if err := lock.TryLock(ctx, maxRetries, retryInterval); err != nil {
		return err
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = lock.Unlock(unlockCtx)
	}()
	return fn()
}
