// Command evalspeech scores speech providers on Indonesian business questions
// read aloud — T-W7's owed live arm, and research 08 §6's unknowns 1 and 2.
//
//	make eval-speech-dry                                  # check the set, $0.00
//	OPENROUTER_API_KEY=… make eval-speech CLIPS=/abs/path/to/recordings
//	OPENROUTER_API_KEY=… make eval-speech CLIPS=… EVAL_ARGS='-providers openrouter,openrouter:openai/gpt-4o-transcribe'
//
// `openrouter:<model>` scores one OpenRouter model under its own label, so one
// key compares several — the reason research 08 §2e gives for scoring OpenRouter
// at all. `groq` and `openai` still take their own keys.
//
// **The set is a reading script** (testdata/eval/speech.yaml). Someone at the
// pilot reads each line and saves it as <id>.<ext>. The recordings are a
// person's voice and stay outside the repository.
//
// Each provider is built with speech.New, exactly as the product builds it, so
// what is scored is the request the voice route sends: the same client, the
// same `verbose_json`, the same language hint. A provider with no key is
// skipped and says so — a run that quietly scored one provider reads like a
// comparison and is not one.
//
// The report is Markdown, to stdout and to -out. The figures that decide
// SPEECH_PROVIDER go into docs/coverage/voice.md, not into the tree.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/evalspeech"
	"github.com/fauzanebd/argentum/internal/speech"
)

func main() {
	var (
		setPath   = flag.String("set", "testdata/eval/speech.yaml", "the reading script")
		clips     = flag.String("clips", "", "directory holding <id>.<ext> recordings")
		providers = flag.String("providers", "openrouter,groq,openai", "providers to score: openrouter, openrouter:<model>, groq, openai, custom")
		baseURL   = flag.String("base-url", "", "for custom: an endpoint that speaks /audio/transcriptions")
		model     = flag.String("model", "", "for custom: the model; for groq or openai, overrides the default")
		out       = flag.String("out", "", "write the Markdown report here as well as to stdout")
		dryRun    = flag.Bool("dry-run", false, "check the set, and the recordings if -clips is given, without calling a provider")
		timeout   = flag.Duration("timeout", 60*time.Second, "per-clip provider timeout")
	)
	flag.Parse()

	set, err := evalspeech.LoadSet(*setPath)
	if err != nil {
		fail(err)
	}

	if *dryRun {
		os.Exit(dry(set, *clips))
	}
	if *clips == "" {
		fail(fmt.Errorf("-clips is required: a directory holding one <id>.<ext> per line of %s", *setPath))
	}

	var ps []evalspeech.Provider
	for _, label := range strings.Split(*providers, ",") {
		label = strings.TrimSpace(label)
		name, pinned, _ := strings.Cut(label, ":")
		cfg := speech.Config{Enabled: true, Provider: name, Model: *model, Timeout: *timeout}
		if pinned != "" {
			cfg.Model = pinned
		}
		switch name {
		case "openrouter":
			cfg.APIKey = os.Getenv("OPENROUTER_API_KEY")
		case "groq":
			cfg.APIKey = os.Getenv("GROQ_API_KEY")
		case "openai":
			cfg.APIKey = os.Getenv("OPENAI_API_KEY")
		case "custom":
			cfg.APIKey, cfg.BaseURL = os.Getenv("SPEECHEVAL_API_KEY"), *baseURL
		default:
			fail(fmt.Errorf("unknown provider %q: openrouter, groq, openai or custom", name))
		}
		if cfg.APIKey == "" {
			fmt.Fprintf(os.Stderr, "skipping %s: no key in the environment\n", label)
			continue
		}
		tr := speech.New(cfg)
		if !tr.Enabled() {
			fmt.Fprintf(os.Stderr, "skipping %s: not usable (see the log line above)\n", label)
			continue
		}
		ps = append(ps, evalspeech.Provider{Name: label, Transcriber: tr})
	}
	if len(ps) == 0 {
		fail(fmt.Errorf("no provider to score: set OPENROUTER_API_KEY, GROQ_API_KEY and/or OPENAI_API_KEY"))
	}

	results := evalspeech.Run(context.Background(), set, *clips, ps, *timeout)
	report := evalspeech.Report(set, results, evalspeech.Pairs)
	fmt.Print(report)
	if *out != "" {
		if err := os.WriteFile(*out, []byte(report), 0o644); err != nil {
			fail(err)
		}
	}
}

// dry checks the set against its own text, and the directory for recordings.
func dry(set *evalspeech.Set, clips string) int {
	code := 0
	for _, c := range set.Clips {
		if got := evalspeech.Numbers(c.Text); !evalspeech.SameNumbers(c.Numbers, got) {
			fmt.Printf("MISMATCH %s: the text reads as %v, the set states %v\n", c.ID, got, c.Numbers)
			code = 1
		}
	}
	fmt.Printf("set: %d clips, language %q, every stated number matches its text: %v\n", len(set.Clips), set.Language, code == 0)
	if clips != "" {
		var missing []string
		for _, c := range set.Clips {
			if _, ok := evalspeech.FindClip(clips, c.ID); !ok {
				missing = append(missing, c.ID)
			}
		}
		fmt.Printf("recordings in %s: %d of %d", clips, len(set.Clips)-len(missing), len(set.Clips))
		if len(missing) > 0 {
			fmt.Printf("; missing: %s", strings.Join(missing, ", "))
		}
		fmt.Println()
	}
	return code
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "evalspeech:", err)
	os.Exit(1)
}
