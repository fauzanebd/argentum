// Command eval scores the analytics agent against a golden question set.
//
//	go run ./cmd/eval -set testdata/eval/golden.yaml
//	go run ./cmd/eval -set testdata/eval/golden.yaml -only indonesian
//	go run ./cmd/eval -set testdata/eval/golden.yaml -model anthropic/claude-haiku-4.5 -out report.json
//	go run ./cmd/eval -models deepseek/deepseek-v3.2,anthropic/claude-haiku-4.5
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
	"github.com/fauzanebd/argentum/internal/tracing"
)

func main() {
	var (
		setPath = flag.String("set", "testdata/eval/golden.yaml", "path to the golden question set")
		model   = flag.String("model", "", "override LLM_MODEL for this run")
		// The matrix (T-Q5). Every quality number this project has published was
		// measured on one model, so "did the prompt help?" and "would another
		// model help more?" have been the same unanswered question.
		//
		// Separate from -model rather than -model taking a list, because the two
		// produce different output: one report, or a comparison. A flag whose
		// return type depends on how many commas are in it is a flag nobody can
		// script against.
		models  = flag.String("models", "", "comma-separated models to score the set against, in order; prints a comparison")
		outPath = flag.String("out", "", "write the full JSON report here")
		only    = flag.String("only", "", "comma-separated case ids or categories to run")
		demoDSN = flag.String("demo-dsn", "postgres://demo:demo@localhost:5433/demo_analytics?sslmode=disable", "DSN of the demo tenant database to seed")
		// Metabase runs inside compose and cannot resolve the host-side
		// address this process uses, so the DSN it is registered with needs a
		// different host:port. Without this the registration is rejected and
		// every dashboard panel fails to execute — which is what the three
		// chart_dashboard cases had been quietly measuring. Set empty to
		// register the DSN unchanged.
		metabaseHost = flag.String("metabase-db-host", "postgres_demo:5432",
			"host:port Metabase should use to reach the demo database")
		timeout = flag.Duration("case-timeout", 3*time.Minute, "per-case timeout")
		dryRun  = flag.Bool("dry-run", false, "validate the set and the tenant, then exit without calling the LLM")
		// The metric registry is state on the reused eval tenant, so a run that
		// wants it absent has to say so — otherwise "with metrics" and "without"
		// are the same run twice (T-07's before/after).
		withMetrics = flag.Bool("metrics", true, "define the tenant's metrics before running; -metrics=false removes them first")
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

	// OTel (T-17), a no-op unless OTEL_EXPORTER_OTLP_ENDPOINT is set — same
	// contract as cmd/api and cmd/worker. This command is the cheapest way to
	// produce a real waterfall: it runs the same turn path as the worker, one
	// question at a time, against a tenant that is already seeded. Without it
	// the spans the turn creates are non-recording and a collector sees
	// nothing, which is exactly what the first attempt at T-17's gate found.
	rootCtx := context.Background()
	shutdownTracing, err := tracing.Init(rootCtx, "argentum-eval", "1")
	if err != nil {
		logrus.WithError(err).Warn("otel: tracing not enabled")
	}
	// The batcher flushes on shutdown, and this command ends with os.Exit(1)
	// whenever a case failed — which `defer` does not survive. So the flush is
	// a named function called on both exits, and a failing run is precisely
	// the run whose trace somebody wants to look at.
	flushTracing := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(ctx); err != nil {
			logrus.WithError(err).Warn("otel: exporter did not flush cleanly")
		}
	}
	defer flushTracing()

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

	// Which models to score. One entry is the ordinary run and produces exactly
	// the report this command has always produced; more than one produces the
	// comparison as well.
	modelList := []string{cfg.LLMModel}
	if *models != "" {
		modelList = splitModels(*models)
		if len(modelList) == 0 {
			fatalf("-models %q named none", *models)
		}
	}

	fmt.Printf("eval set:   %s (%d cases", *setPath, len(set.Cases))
	if len(cases) != len(set.Cases) {
		fmt.Printf(", running %d", len(cases))
	}
	fmt.Printf(")\ncategories: %s\n", formatCategories(set.Categories()))
	fmt.Printf("models:     %s\n\n", strings.Join(modelList, ", "))

	reports := make([]eval.Report, 0, len(modelList))
	for _, m := range modelList {
		rep, ok := runOneModel(ctx, cfg, m, set, cases, runOpts{
			demoDSN:      *demoDSN,
			metabaseHost: *metabaseHost,
			withMetrics:  *withMetrics,
			timeout:      *timeout,
			dryRun:       *dryRun,
			showModel:    len(modelList) > 1,
		})
		if !ok {
			return // dry run: the tenant and set were validated, nothing was spent.
		}
		reports = append(reports, rep)
		fmt.Print(rep.Text())
	}

	// The comparison, and the reason -models exists. Skipped for a single model,
	// where it would restate the report above it.
	var matrix *eval.Matrix
	if len(reports) > 1 {
		matrix = &eval.Matrix{Set: set.Name, Models: modelList, Reports: reports}
		fmt.Print(matrix.Text())
	}

	if *outPath != "" {
		var payload any = reports[0]
		if matrix != nil {
			payload = matrix
		}
		raw, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			fatalf("marshal report: %v", err)
		}
		if err := os.WriteFile(*outPath, raw, 0o644); err != nil {
			fatalf("write report: %v", err)
		}
		fmt.Printf("report written to %s\n", *outPath)
	}

	// Non-zero exit when anything failed on any model, so CI can gate on it
	// later without a wrapper script.
	for _, rep := range reports {
		if rep.Failed > 0 {
			flushTracing()
			os.Exit(1)
		}
	}
}

// runOpts is what one model's run needs beyond the set. A struct because the
// alternative is nine positional parameters, four of which are bools.
type runOpts struct {
	demoDSN      string
	metabaseHost string
	withMetrics  bool
	timeout      time.Duration
	dryRun       bool
	// showModel prefixes each case line with the model, which is noise on a
	// single-model run and the only way to read the output on a matrix one.
	showModel bool
}

// runOneModel scores the whole set against one model and returns its report.
// ok is false only for a dry run, which validates and spends nothing.
//
// A fresh stack per model, closed before the next one starts. The model is read
// out of config when the LLM clients are built, so switching it means rebuilding
// them — and sharing a stack across models would leave the second run scoring
// the first model's clients, which is the kind of bug that produces a
// suspiciously identical pass rate and no error at all.
func runOneModel(
	ctx context.Context, cfg *config.Config, model string,
	set *eval.Set, cases []eval.Case, opts runOpts,
) (eval.Report, bool) {
	cfg.LLMModel = model

	stack, err := bootstrap.New(ctx, cfg)
	if err != nil {
		fatalf("bootstrap (%s): %v", model, err)
	}
	defer stack.Close()

	// Idempotent, and re-run per model on purpose: it is what guarantees each
	// model meets the same tenant, the same sources and the same metric
	// registry. Cheap next to one LLM call.
	tenant, err := eval.EnsureTenant(ctx, stack, opts.demoDSN, opts.metabaseHost, opts.withMetrics)
	if err != nil {
		fatalf("seed eval tenant: %v", err)
	}

	if opts.dryRun {
		fmt.Printf("tenant:     %s (%s)\n", tenant.CompanyName, tenant.CompanyID)
		fmt.Println("dry run: set and tenant validated, no LLM calls made.")
		return eval.Report{}, false
	}

	fmt.Printf("--- %s ---\ntenant: %s (%s)\n", model, tenant.CompanyName, tenant.CompanyID)

	runner := eval.NewRunner(stack, tenant, opts.timeout)
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
	return eval.Summarize(set.Name, model, started, results), true
}

// splitModels parses the -models list, dropping blanks and duplicates. A
// duplicate would run the same model twice and put two identical columns in the
// comparison, which reads as a bug in the harness rather than in the flag.
func splitModels(raw string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	for _, m := range strings.Split(raw, ",") {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
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
