// Argentum API server: control plane + queue producer + WebSocket fan-out.
//
// The API process never runs the agent itself  — it resolves threads,
// persists user messages, enqueues `chat:run` tasks via asynq, and
// streams worker-published events back to dashboard clients via Redis
// pub/sub. cmd/worker handles the heavy lifting.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	_ "github.com/fauzanebd/argentum/internal/adapters/db/mysql"
	_ "github.com/fauzanebd/argentum/internal/adapters/db/postgres"
	"github.com/fauzanebd/argentum/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		logrus.Fatalf("Failed to load config: %v", err)
	}
	setupLogging(cfg)
	logrus.Info("Argentum API server starting")

	rootCtx := context.Background()
	deps, err := bootstrap(rootCtx, cfg)
	if err != nil {
		logrus.Fatal(err)
	}
	defer deps.cleanup()

	router := newRouter(deps)
	srv := &http.Server{Addr: ":" + strconv.Itoa(cfg.Port), Handler: router}
	go func() {
		logrus.Infof("Listening on :%d", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logrus.Fatalf("server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logrus.Info("Shutting down…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	logrus.Info("Bye")
}

func setupLogging(cfg *config.Config) {
	level, _ := logrus.ParseLevel(cfg.LogLevel)
	logrus.SetLevel(level)
	logrus.SetFormatter(&logrus.JSONFormatter{})
}
