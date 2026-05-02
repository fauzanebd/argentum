package main

import (
	"strings"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/Ingenimax/agent-sdk-go/pkg/llm/openai"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/config"
)

func buildRedisClient(cfg *config.Config) *redis.Client {
	if cfg.RedisURL == "" {
		return nil
	}
	url := cfg.RedisURL
	// go-redis ParseURL requires the redis:// scheme; fall back to a
	// bare-address client for "host:port" style values.
	if !strings.Contains(url, "://") {
		return redis.NewClient(&redis.Options{Addr: url, Password: cfg.RedisPassword})
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		logrus.WithError(err).Warn("redis: invalid REDIS_URL; using bare addr")
		return redis.NewClient(&redis.Options{Addr: url, Password: cfg.RedisPassword})
	}
	if cfg.RedisPassword != "" {
		opt.Password = cfg.RedisPassword
	}
	return redis.NewClient(opt)
}

func buildLLM(cfg *config.Config) interfaces.LLM {
	opts := []openai.Option{}
	if cfg.LLMModel != "" {
		opts = append(opts, openai.WithModel(cfg.LLMModel))
	}
	if cfg.LLMBaseURL != "" {
		opts = append(opts, openai.WithBaseURL(cfg.LLMBaseURL))
	}
	return openai.NewClient(cfg.LLMAPIKey, opts...)
}
