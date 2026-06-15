// Redis 客户端 + 分布式锁 + 缓存辅助
package dal

import (
	"context"
	"errors"
	"time"

	"github.com/api-ops/api-ops/internal/config"
	"github.com/redis/go-redis/v9"
)

var RDB *redis.Client

// InitRedis 初始化 Redis 连接
func InitRedis(cfg *config.Config) error {
	RDB = redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := RDB.Ping(ctx).Err(); err != nil {
		return err
	}
	return nil
}

// CloseRedis 关闭 Redis 连接
func CloseRedis() {
	if RDB != nil {
		_ = RDB.Close()
	}
}

// CacheGetOrSet 通用的"读取或计算"缓存
// 如果 cacheHit == true 则返回缓存值；否则调用 compute() 并把结果写入缓存
// key 前缀建议 "upstream:ops:"
func CacheGetOrSet(ctx context.Context, key string, ttl time.Duration, compute func() (interface{}, error)) (interface{}, bool, error) {
	if RDB == nil {
		v, err := compute()
		return v, false, err
	}
	val, err := RDB.Get(ctx, key).Result()
	if err == nil {
		return val, true, nil
	}
	if !errors.Is(err, redis.Nil) {
		// Redis 故障，降级到直接计算
		v, cerr := compute()
		return v, false, cerr
	}
	v, err := compute()
	if err != nil {
		return nil, false, err
	}
	_ = RDB.Set(ctx, key, v, ttl).Err()
	return v, false, nil
}

// LockOptions 分布式锁选项
type LockOptions struct {
	Key   string
	TTL   time.Duration
	Retry time.Duration
}

// TryLock 非阻塞获取锁；成功返回 release func
func TryLock(ctx context.Context, opts LockOptions) (bool, func(), error) {
	if RDB == nil {
		// 没 Redis 时不阻塞，允许重复执行（开发模式）
		return true, func() {}, nil
	}
	ok, err := RDB.SetNX(ctx, "lock:"+opts.Key, "1", opts.TTL).Result()
	if err != nil {
		return false, nil, err
	}
	if !ok {
		return false, nil, nil
	}
	release := func() {
		// 用 Lua 脚本保证只删自己的
		const lua = `
if redis.call("get", KEYS[1]) == ARGV[1] then
 return redis.call("del", KEYS[1])
else
 return 0
end`
		_ = RDB.Eval(context.Background(), lua, []string{"lock:" + opts.Key}, "1").Err()
	}
	return true, release, nil
}
