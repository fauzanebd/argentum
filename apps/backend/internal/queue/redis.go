package queue

import (
	"fmt"
	"strings"

	"github.com/hibiken/asynq"
)

// BuildRedisOpt accepts either a "redis://user:pass@host:port/db" URI or a
// bare "host:port" form (which asynq's RedisClientOpt expects natively).
// Password is overlaid only when the URI form doesn't include one.
func BuildRedisOpt(url, password string) (asynq.RedisConnOpt, error) {
	if url == "" {
		return nil, fmt.Errorf("redis url is empty")
	}
	if strings.Contains(url, "://") {
		opt, err := asynq.ParseRedisURI(url)
		if err != nil {
			return nil, fmt.Errorf("parse redis uri: %w", err)
		}
		if cli, ok := opt.(asynq.RedisClientOpt); ok && cli.Password == "" && password != "" {
			cli.Password = password
			return cli, nil
		}
		return opt, nil
	}
	return asynq.RedisClientOpt{Addr: url, Password: password}, nil
}
