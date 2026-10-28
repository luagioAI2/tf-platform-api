package rds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var structCache sync.Map // 缓存反射信息，避免每次请求重复解析 tag

type fieldInfo struct {
	RedisTag string
	Index    int
}

// StructToRedisHashOptimized 把 struct 按 `redis` tag 转成 map，用于 HSet。
// 空字符串 / nil 指针会被跳过（不覆盖已有值）。
func StructToRedisHashOptimized(input interface{}) (map[string]string, error) {
	v := reflect.ValueOf(input)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil, errors.New("input is nil pointer")
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("input must be a struct, got %s", v.Kind())
	}

	t := v.Type()
	cached, ok := structCache.Load(t)
	if !ok {
		cached, _ = structCache.LoadOrStore(t, buildFieldCache(t))
	}
	cache := cached.([]fieldInfo)

	result := make(map[string]string, len(cache))
	for _, info := range cache {
		fValue := v.Field(info.Index)
		if isZero(fValue) {
			continue
		}
		strVal, err := toString(fValue)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", t.Field(info.Index).Name, err)
		}
		result[info.RedisTag] = strVal
	}
	return result, nil
}

func buildFieldCache(t reflect.Type) []fieldInfo {
	cache := make([]fieldInfo, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		redisTag := field.Tag.Get("redis")
		if redisTag == "" || redisTag == "-" {
			continue
		}
		cache = append(cache, fieldInfo{RedisTag: redisTag, Index: i})
	}
	return cache
}

// isZero 判断是否需要跳过该字段
func isZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return v.String() == ""
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	case reflect.Struct:
		if t, ok := v.Interface().(time.Time); ok {
			return t.IsZero()
		}
		return false
	default:
		return false
	}
}

// toString 统一转字符串
func toString(v reflect.Value) (string, error) {
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return "", nil
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.String:
		return v.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'f', -1, 64), nil
	case reflect.Bool:
		return strconv.FormatBool(v.Bool()), nil
	case reflect.Struct:
		if t, ok := v.Interface().(time.Time); ok {
			return t.Format(time.RFC3339Nano), nil
		}
		if b, err := json.Marshal(v.Interface()); err == nil {
			return string(b), nil
		}
		return "", fmt.Errorf("unsupported struct type: %s", v.Type())
	default:
		return "", fmt.Errorf("unsupported type: %s", v.Type())
	}
}

// ============================ JSON 缓存辅助 ============================

// SetJSON 序列化后写入 Redis
func SetJSON(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	c := GetClient()
	if c == nil {
		return ErrRedisUnavailable
	}
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal cache value: %w", err)
	}
	return c.Set(ctx, key, b, ttl).Err()
}

// GetJSON 读取并反序列化；key 不存在返回 (false, nil)
func GetJSON(ctx context.Context, key string, out interface{}) (bool, error) {
	c := GetClient()
	if c == nil {
		return false, ErrRedisUnavailable
	}
	b, err := c.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return false, fmt.Errorf("unmarshal cache value: %w", err)
	}
	return true, nil
}

// Del 删除缓存
func Del(ctx context.Context, keys ...string) error {
	c := GetClient()
	if c == nil {
		return ErrRedisUnavailable
	}
	if len(keys) == 0 {
		return nil
	}
	return c.Del(ctx, keys...).Err()
}
