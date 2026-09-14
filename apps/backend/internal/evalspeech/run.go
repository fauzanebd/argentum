package evalspeech

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/speech"
)

// Provider is one speech endpoint under test, built by speech.New exactly as the
// product builds it — the same request, the same language hint.
type Provider struct {
	Name        string
	Transcriber speech.Transcriber
}

// Result is one provider's run over a set.
type Result struct {
	Provider string
	Model    string
	Scores   []Score
	// Missing are clips with no recording in the directory; Failed are clips the
	// provider refused or the file was not the audio its name says.
	Missing      []string
	Failed       map[string]string
	AudioSeconds float64
}

// extTypes is the media type a recording is declared as, from its extension —
// what a browser would have put on the upload.
var extTypes = map[string]string{
	".webm": "audio/webm", ".ogg": "audio/ogg", ".m4a": "audio/mp4", ".mp4": "audio/mp4",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".flac": "audio/flac",
}

// FindClip is the recording for a clip id: <id>.<ext> in dir.
func FindClip(dir, id string) (string, bool) {
	matches, _ := filepath.Glob(filepath.Join(dir, id+".*"))
	sort.Strings(matches)
	for _, m := range matches {
		if _, ok := extTypes[strings.ToLower(filepath.Ext(m))]; ok {
			return m, true
		}
	}
	return "", false
}

// Run sends every recording in dir to every provider and scores what comes back.
// A clip is sent the way the voice route sends it: its type accepted against its
// bytes, and the set's language as the hint.
func Run(ctx context.Context, set *Set, dir string, providers []Provider, perClip time.Duration) []Result {
	results := make([]Result, 0, len(providers))
	for _, p := range providers {
		r := Result{Provider: p.Name, Model: p.Transcriber.Model(), Failed: map[string]string{}}
		for _, c := range set.Clips {
			path, ok := FindClip(dir, c.ID)
			if !ok {
				r.Missing = append(r.Missing, c.ID)
				continue
			}
			audio, err := os.ReadFile(path)
			if err != nil {
				r.Failed[c.ID] = err.Error()
				continue
			}
			mediaType, _, accepted := speech.Accept(extTypes[strings.ToLower(filepath.Ext(path))], audio)
			if !accepted {
				r.Failed[c.ID] = "the file is not the audio its extension names"
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, perClip)
			heard, err := p.Transcriber.Transcribe(cctx, bytes.NewReader(audio), mediaType, set.Language)
			cancel()
			if err != nil {
				r.Failed[c.ID] = err.Error()
				continue
			}
			r.AudioSeconds += heard.Seconds
			r.Scores = append(r.Scores, ScoreClip(c, heard.Text))
		}
		results = append(results, r)
	}
	return results
}

// Report renders results as Markdown: a summary per provider, then every clip.
// The pairs line is the one the decision rests on — the clips whose number
// differs from a neighbour's by one syllable.
func Report(set *Set, results []Result, pairs [][2]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Speech set — %d clips, language %q\n\n", len(set.Clips), set.Language)
	b.WriteString("| Provider | Model | Scored | Missing | Failed | Mean WER | Numbers right | Pairs both right | Digit runs / word runs | Audio s |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, r := range results {
		var werSum float64
		right, digits, words := 0, 0, 0
		byID := map[string]Score{}
		withNumbers := 0
		for _, s := range r.Scores {
			werSum += s.WER
			byID[s.ClipID] = s
			if len(s.Said) > 0 {
				withNumbers++
				if s.NumbersRight {
					right++
				}
			}
			digits += s.DigitRuns
			words += s.WordRuns
		}
		mean := "—"
		if len(r.Scores) > 0 {
			mean = fmt.Sprintf("%.1f%%", 100*werSum/float64(len(r.Scores)))
		}
		pairsRight, pairsScored := 0, 0
		for _, pr := range pairs {
			a, okA := byID[pr[0]]
			z, okZ := byID[pr[1]]
			if !okA || !okZ {
				continue
			}
			pairsScored++
			if a.NumbersRight && z.NumbersRight {
				pairsRight++
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %d | %s | %d / %d | %d / %d | %d / %d | %.1f |\n",
			r.Provider, r.Model, len(r.Scores), len(r.Missing), len(r.Failed), mean,
			right, withNumbers, pairsRight, pairsScored, digits, words, r.AudioSeconds)
	}
	for _, r := range results {
		fmt.Fprintf(&b, "\n## %s (%s)\n\n| Clip | WER | Said | Heard | Numbers | Transcript |\n| --- | --- | --- | --- | --- | --- |\n", r.Provider, r.Model)
		for _, s := range r.Scores {
			mark := "✅"
			if !s.NumbersRight {
				mark = "❌"
			}
			fmt.Fprintf(&b, "| `%s` | %.0f%% | %s | %s | %s | %s |\n",
				s.ClipID, 100*s.WER, values(s.Said), values(s.Heard), mark, strings.ReplaceAll(s.Transcript, "|", "\\|"))
		}
		if len(r.Missing) > 0 {
			fmt.Fprintf(&b, "\n**Missing recordings:** %s\n", strings.Join(r.Missing, ", "))
		}
		ids := make([]string, 0, len(r.Failed))
		for id := range r.Failed {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Fprintf(&b, "\n**Failed `%s`:** %s\n", id, r.Failed[id])
		}
	}
	return b.String()
}

func values(v []float64) string {
	if len(v) == 0 {
		return "—"
	}
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = fmt.Sprintf("%g", x)
	}
	return strings.Join(parts, ", ")
}
