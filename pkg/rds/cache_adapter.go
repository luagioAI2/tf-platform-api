package rds

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache 实现 pkg/cache 的 RedisClient 接口，把 Redis 适配成缓存后端。
type RedisCache struct {
	client *redis.Client
}

// NewRedisCache 创建 Redis 缓存适配器
func NewRedisCache(client *redis.Client) *RedisCache {
	if client == nil {
		client = GetClient()
	}
	return &RedisCache{client: client}
}

// Available 是否可用
func (r *RedisCache) Available() bool { return r != nil && r.client != nil }

// GetBytes 读取
func (r *RedisCache) GetBytes(ctx context.Context, key string) ([]byte, error) {
	if r.client == nil {
		return nil, ErrRedisUnavailable
	}
	b, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

// SetBytes 写入
func (r *RedisCache) SetBytes(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if r.client == nil {
		return ErrRedisUnavailable
	}
	return r.client.Set(ctx, key, val, ttl).Err()
}

// Del 删除
func (r *RedisCache) Del(ctx context.Context, keys ...string) error {
	if r.client == nil {
		return ErrRedisUnavailable
	}
	if len(keys) == 0 {
		return nil
	}
	return r.client.Del(ctx, keys...).Err()
}

// Incr 自增（用于计数器）
func (r *RedisCache) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	if r.client == nil {
		return 0, ErrRedisUnavailable
	}
	pipe := r.client.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return incr.Val(), nil
}

// Client 返回底层客户端
func (r *RedisCache) Client() *redis.Client { return r.client }
