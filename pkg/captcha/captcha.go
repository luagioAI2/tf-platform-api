package captcha

import (
	"context"
	"time"

	"github.com/mojocn/base64Captcha"
	redis "github.com/redis/go-redis/v9"
)

func NewDefaultRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{
		Expiration: time.Second * 180,
		PreKey:     "CAPTCHA_",
		Context:    context.TODO(),
		Client:     client,
	}
}

type RedisStore struct {
	Expiration time.Duration
	PreKey     string
	Context    context.Context
	Client     *redis.Client
}

func (rs *RedisStore) UseWithCtx(ctx context.Context) base64Captcha.Store {
	rs.Context = ctx
	return rs
}

func (rs *RedisStore) Set(id string, value string) error {
	err := rs.Client.Set(rs.Context, rs.PreKey+id, value, rs.Expiration).Err()
	if err != nil {
		return err
	}
	return nil
}

func (rs *RedisStore) Get(key string, clear bool) string {
	val, err := rs.Client.Get(rs.Context, key).Result()
	if err != nil {
		return ""
	}
	if clear {
		err := rs.Client.Del(rs.Context, key).Err()
		if err != nil {
			return ""
		}
	}
	return val
}

func (rs *RedisStore) Verify(id, answer string, clear bool) bool {
	key := rs.PreKey + id
	v := rs.Get(key, clear)
	return v == answer
}
