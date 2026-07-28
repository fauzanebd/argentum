// Command eval scores the analytics agent against a golden question set.
//
//	go run ./cmd/eval -set testdata/eval/golden.yaml
//	go run ./cmd/eval -set testdata/eval/golden.yaml -only indonesian
//	go run ./cmd/eval -set testdata/eval/golden.yaml -model anthropic/claude-haiku-4.5 -out report.json
//
// It runs the real agent — same factory, tools, guardrails and prompt as the
// worker, wired by internal/bootstrap — against a seeded tenant on the demo
// star schema. That means it costs real LLM tokens and needs local infra up
// (`make dev-infra`).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/sirupsen/logrus"

	_ "github.com/fauzanebd/argentum/internal/adapters/db/mysql"
	_ "github.com/fauzanebd/argentum/internal/adapters/db/postgres"
	_ "github.com/fauzanebd/argentum/internal/adapters/db/sqlserver"
	"github.com/fauzanebd/argentum/internal/bootstrap"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/eval"
)

func main() {
	var (
		setPath = flag.String("set", "testdata/eval/golden.yaml", "path to the golden question set")
		model   = flag.String("model", "", "override LLM_MODEL for this run")
		outPath = flag.String("out", "", "write the full JSON report here")
		only    = flag.String("only", "", "comma-separated case ids or categories to run")
		demoDSN = flag.String("demo-dsn", "postgres://demo:demo@localhost:5433/demo_analytics?sslmode=disable", "DSN of the demo tenant database to seed")
		// Metabase runs inside compose and cannot resolve the host-side
		// address this process uses, so the DSN it is registered with needs a
		// different host:port. Without this the registration is rejected and
		// every create_visualization call fails — which is what the three
		// chart_dashboard cases had been quietly measuring. Set empty to
		// register the DSN unchanged.
		metabaseHost = flag.String("metabase-db-host", "postgres_demo:5432",
			"host:port Metabase should use to reach the demo database")
		timeout     = flag.Duration("case-timeout", 3*time.Minute, "per-case timeout")
		dryRun      = flag.Bool("dry-run", false, "validate the set and the tenant, then exit without calling the LLM")
		allowRemote = flag.Bool("allow-remote-db", false, "permit running against a non-local control database")
		verbose     = flag.Bool("v", false, "keep the agent SDK's debug logging on stdout")
	)
	flag.Parse()

	logrus.SetLevel(logrus.WarnLevel)
	logrus.SetFormatter(&logrus.TextFormatter{})

	// agent-sdk-go logs every request, tool call and streaming chunk to
	// stdout at debug level through zerolog, which buries the report this
	// command exists to print. The whole point of `make eval` is an output
	// a human pastes into a ticket gate, so quiet it unless asked.
	if !*verbose {
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	}

	cfg, err := config.Load()
	if err != nil {
		fatalf("load config: %v", err)
	}
	if *model != "" {
		cfg.LLMModel = *model
	}

	// Finding E-2: the working .env on the developer machine pointed
	// DB_HOST at a deployed control plane while looking local. The eval
	// harness writes companies, users, threads, messages and usage rows on
	// every run — doing that to production because of a stale env file is
	// a mistake worth making impossible rather than documenting.
	if !*allowRemote {
		if host, remote := nonLocalHost(cfg.DBHost); remote {
			fatalf("refusing to run: DB_HOST=%s is not a local address.\n"+
				"The eval harness writes test data (companies, threads, messages) into the control DB.\n"+
				"Run it against local infra:\n"+
				"  DB_HOST=localhost DB_PORT=5432 REDIS_URL=localhost:6385 make eval\n"+
				"or pass -allow-remote-db if you genuinely mean %s.", host, host)
		}
	}

	set, err := eval.LoadSet(*setPath)
	if err != nil {
		fatalf("%v", err)
	}
	cases := set.Cases
	if *only != "" {
		cases = set.Filter(strings.Split(*only, ","))
		if len(cases) == 0 {
			fatalf("-only %q matched no cases", *only)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stack, err := bootstrap.New(ctx, cfg)
	if err != nil {
		fatalf("bootstrap: %v", err)
	}
	defer stack.Close()

	tenant, err := eval.EnsureTenant(ctx, stack, *demoDSN, *metabaseHost)
	if err != nil {
		fatalf("seed eval tenant: %v", err)
	}

	fmt.Printf("eval set:   %s (%d cases", *setPath, len(set.Cases))
	if len(cases) != len(set.Cases) {
		fmt.Printf(", running %d", len(cases))
	}
	fmt.Printf(")\ncategories: %s\n", formatCategories(set.Categories()))
	fmt.Printf("tenant:     %s (%s)\n", tenant.CompanyName, tenant.CompanyID)
	fmt.Printf("model:      %s\n\n", cfg.LLMModel)

	if *dryRun {
		fmt.Println("dry run: set and tenant validated, no LLM calls made.")
		return
	}

	runner := eval.NewRunner(stack, tenant, *timeout)
	started := time.Now()
	results := make([]eval.Result, 0, len(cases))

	// Sequential on purpose. The cases share one tenant and one LLM
	// account; running them in parallel trades a reproducible latency and
	// cost profile — two of the four numbers this harness reports — for a
	// few minutes of wall clock.
	for i, c := range cases {
		fmt.Printf("[%2d/%2d] %-28s ", i+1, len(cases), c.ID)
		res := runner.RunCase(ctx, c)
		results = append(results, res)
		if res.Passed {
			fmt.Printf("PASS  (%.1fs)\n", float64(res.DurationMS)/1000)
		} else {
			fmt.Printf("FAIL  (%.1fs)  %s\n", float64(res.DurationMS)/1000, firstFailure(res))
		}
	}

	report := eval.Summarize(set.Name, cfg.LLMModel, started, results)
	fmt.Print(report.Text())

	if *outPath != "" {
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fatalf("marshal report: %v", err)
		}
		if err := os.WriteFile(*outPath, raw, 0o644); err != nil {
			fatalf("write report: %v", err)
		}
		fmt.Printf("report written to %s\n", *outPath)
	}

	// Non-zero exit when anything failed, so CI can gate on it later
	// without a wrapper script.
	if report.Failed > 0 {
		os.Exit(1)
	}
}

// nonLocalHost reports whether host is something other than loopback or a
// private address. An empty host means the config default, which is local.
func nonLocalHost(host string) (string, bool) {
	h := strings.TrimSpace(host)
	if h == "" || h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "host.docker.internal" {
		return h, false
	}
	if ip := net.ParseIP(h); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() {
			return h, false
		}
	}
	return h, true
}

func formatCategories(counts map[string]int) string {
	parts := make([]string, 0, len(counts))
	for name, n := range counts {
		parts = append(parts, fmt.Sprintf("%s=%d", name, n))
	}
	return strings.Join(parts, " ")
}

func firstFailure(r eval.Result) string {
	if len(r.Failures) == 0 {
		return ""
	}
	return r.Failures[0]
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "eval: "+format+"\n", args...)
	os.Exit(2)
}
