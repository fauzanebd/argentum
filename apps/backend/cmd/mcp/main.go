// Argentum MCP server: the tenant's own agent, calling our tools (T-14).
//
// A third process rather than a route on cmd/api, for the reason cmd/discord is
// a third process: it speaks a different protocol on a different port with a
// different failure mode, and an MCP session that hangs should not be able to
// exhaust the dashboard's connection budget. It shares everything that matters
// — the tool registry, the tenant pool, the audit sink, the metering path — by
// building the same internal/bootstrap stack the worker does, so a tool called
// from Claude Code is the tool the agent runs.
package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	_ "github.com/fauzanebd/argentum/internal/adapters/db/mysql"
	_ "github.com/fauzanebd/argentum/internal/adapters/db/postgres"
	_ "github.com/fauzanebd/argentum/internal/adapters/db/sqlserver"
	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/bootstrap"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/mcpserver"
	"github.com/fauzanebd/argentum/internal/tools"
	"github.com/fauzanebd/argentum/internal/tracing"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		logrus.Fatalf("Failed to load config: %v", err)
	}
	level, _ := logrus.ParseLevel(cfg.LogLevel)
	logrus.SetLevel(level)
	logrus.SetFormatter(&logrus.JSONFormatter{})

	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()

	// OTel (T-17). A no-op unless OTEL_EXPORTER_OTLP_ENDPOINT is set, so a
	// deployment with no collector runs exactly as it did.
	shutdownTracing, err := tracing.Init(rootCtx, "argentum-mcp", "1")
	if err != nil {
		logrus.WithError(err).Warn("otel: tracing not enabled")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(ctx); err != nil {
			logrus.WithError(err).Warn("otel: exporter did not flush cleanly")
		}
	}()

	stack, err := bootstrap.New(rootCtx, cfg)
	if err != nil {
		logrus.Fatalf("bootstrap: %v", err)
	}
	defer stack.Close()

	handler := mcpserver.Handler(stack.Tools, app.NewAPIKeyService(pgctl.NewAPIKeyRepo(stack.ControlDB)))

	srv := &http.Server{
		Addr:    cfg.MCPServerAddr,
		Handler: handler,
		// An MCP session is long-lived by design, so there is no read or write
		// timeout here — the streamable transport holds a response open. What is
		// bounded is the header read, which is the part an idle connection can
		// abuse without ever becoming a session.
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Bind before announcing. ListenAndServe inside the goroutine used to mean
	// "listening" was logged by a process whose port was already taken, and the
	// fatal that followed it read as a second, unrelated event — which is exactly
	// how the 2026-08-04 gate spent its first attempt talking to somebody else's
	// service on :8081.
	ln, err := net.Listen("tcp", cfg.MCPServerAddr)
	if err != nil {
		logrus.Fatalf("mcp server: %v", err)
	}

	fields := logrus.Fields{
		"addr":  cfg.MCPServerAddr,
		"tools": mcpserver.ExposedTools(),
		"of":    tools.Names(stack.Tools),
	}
	// A name on the surface that the registry does not hold is a tool the setup
	// guide promises and no client can call. Absent-because-unconfigured looks
	// the same from here as absent-because-nobody-wrote-it, so it is logged
	// rather than judged.
	if missing := mcpserver.Missing(stack.Tools); len(missing) > 0 {
		fields["missing"] = missing
		logrus.WithFields(fields).Warn("Argentum MCP server listening, with tools on the surface that this deployment does not run")
	} else {
		logrus.WithFields(fields).Info("Argentum MCP server listening")
	}

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logrus.Fatalf("mcp server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logrus.Info("Shutting down…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logrus.WithError(err).Warn("mcp server did not shut down cleanly")
	}
	logrus.Info("Bye")
}
