package evalspeech

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/speech"
)

func TestNumbersReadsDigitsAndIndonesianWordsAlike(t *testing.T) {
	cases := []struct {
		text string
		want []float64
	}{
		{"lebih dari tiga ratus juta rupiah", []float64{3e8}},
		{"lebih dari 300 juta rupiah", []float64{3e8}},
		{"lebih dari tiga puluh juta rupiah", []float64{3e7}},
		{"di atas Rp 2,5 juta", []float64{2.5e6}},
		{"di atas dua koma lima juta", []float64{2.5e6}},
		{"tahun dua ribu dua puluh lima", []float64{2025}},
		{"retur di atas seratus lima puluh ribu rupiah selama tiga bulan", []float64{150000, 3}},
		{"kurang dari empat belas kali", []float64{14}},
		{"kurang dari empat puluh kali", []float64{40}},
		{"voucher Rp50.000 yang", []float64{50000}},
		{"naik lebih dari 12% sejak Juli", []float64{12}},
		{"sebesar 7,5 persen", []float64{7.5}},
		{"sebesar tujuh koma lima persen", []float64{7.5}},
		{"a drop of 7.5 percent", []float64{7.5}},
		{"target 1.2 billion", []float64{1.2e9}},
		{"sepuluh pelanggan", []float64{10}},
		{"ke seribu rupiah terdekat", []float64{1000}},
		{"untuk 4.200 transaksi", []float64{4200}},
		{"satu juta dua ratus ribu", []float64{1.2e6}},
		{"dua liter di bawah dua puluh ribu", []float64{2, 20000}},
		// An ordinal is not a figure anybody asked about.
		{"kuartal ke-3 tahun ini", []float64{}},
		{"Berapa total penjualan kemarin di cabang Kemang?", []float64{}},
	}
	for _, c := range cases {
		if got := Numbers(c.text); !SameNumbers(c.want, got) {
			t.Errorf("Numbers(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// The set's stated numbers and its text must agree, or the reference scores
// against itself.
func TestTheSetStatesWhatItsTextSays(t *testing.T) {
	set, err := LoadSet(filepath.Join("..", "..", "testdata", "eval", "speech.yaml"))
	if err != nil {
		t.Fatalf("LoadSet: %v", err)
	}
	if len(set.Clips) != 20 || set.Language != "id" {
		t.Fatalf("set has %d clips in %q, want 20 in \"id\"", len(set.Clips), set.Language)
	}
	for _, c := range set.Clips {
		if got := Numbers(c.Text); !SameNumbers(c.Numbers, got) {
			t.Errorf("%s: the text reads as %v, the set states %v", c.ID, got, c.Numbers)
		}
		if _, ok := FindClip(t.TempDir(), c.ID); ok {
			t.Errorf("%s: found a recording in an empty directory", c.ID)
		}
	}
	for _, pr := range Pairs {
		for _, id := range pr {
			found := false
			for _, c := range set.Clips {
				found = found || c.ID == id
			}
			if !found {
				t.Errorf("pair names %q, which is not in the set", id)
			}
		}
	}
}

// Decision 13's case, scored: writing the number differently is not an error;
// hearing a different number is, and WER alone would call it one word.
func TestAMisheardMultiplierIsWrongAndAChangeOfFormIsNot(t *testing.T) {
	line := Clip{ID: "revenue-300m", Text: "Apakah omzet bulan lalu lebih dari tiga ratus juta rupiah?", Numbers: []float64{3e8}}

	form := ScoreClip(line, "Apakah omzet bulan lalu lebih dari 300 juta rupiah?")
	if !form.NumbersRight || form.WER != 0 || form.DigitRuns != 1 || form.WordRuns != 0 {
		t.Errorf("a change of form: %+v; want numbers right, WER 0, one digit run", form)
	}

	misheard := ScoreClip(line, "Apakah omzet bulan lalu lebih dari tiga puluh juta rupiah?")
	if misheard.NumbersRight {
		t.Errorf("tiga puluh juta for tiga ratus juta scored as right: %+v", misheard)
	}
	if misheard.WER == 0 || misheard.WER > 0.2 {
		t.Errorf("WER = %.2f; want one token of eight wrong, which is what makes WER alone blind to it", misheard.WER)
	}
}

func TestWER(t *testing.T) {
	cases := []struct {
		ref, hyp string
		want     float64
	}{
		{"berapa stok gudang", "berapa stok gudang", 0},
		{"berapa stok gudang", "berapa stock gudang", 1.0 / 3},
		{"berapa stok gudang", "berapa gudang", 1.0 / 3},
		{"berapa stok gudang", "", 1},
		{"", "", 0},
	}
	for _, c := range cases {
		if got := WER(Words(c.ref), Words(c.hyp)); got != c.want {
			t.Errorf("WER(%q, %q) = %v, want %v", c.ref, c.hyp, got, c.want)
		}
	}
}

type fixedTranscriber struct {
	heard map[int]string // by byte length, since the clip id does not travel
	calls int
	lang  string
}

func (f *fixedTranscriber) Transcribe(_ context.Context, r io.Reader, _ string, lang string) (speech.Transcript, error) {
	f.calls++
	f.lang = lang
	b, _ := io.ReadAll(r)
	return speech.Transcript{Text: f.heard[len(b)], Seconds: 2}, nil
}
func (f *fixedTranscriber) Enabled() bool { return true }
func (f *fixedTranscriber) Model() string { return "fixed" }

func TestRunScoresWhatIsThereAndSaysWhatIsNot(t *testing.T) {
	dir := t.TempDir()
	webm := func(n int) []byte { return append([]byte{0x1A, 0x45, 0xDF, 0xA3}, make([]byte, n)...) }
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "revenue-300m.webm"), webm(10), 0o644))
	must(os.WriteFile(filepath.Join(dir, "revenue-30m.webm"), webm(20), 0o644))
	must(os.WriteFile(filepath.Join(dir, "sold-14.webm"), []byte("%PDF-1.7 not audio"), 0o644))

	set := &Set{Language: "id", Clips: []Clip{
		{ID: "revenue-300m", Text: "lebih dari tiga ratus juta", Numbers: []float64{3e8}},
		{ID: "revenue-30m", Text: "lebih dari tiga puluh juta", Numbers: []float64{3e7}},
		{ID: "sold-14", Text: "empat belas kali", Numbers: []float64{14}},
		{ID: "sold-40", Text: "empat puluh kali", Numbers: []float64{40}},
	}}
	tr := &fixedTranscriber{heard: map[int]string{14: "lebih dari 300 juta", 24: "lebih dari tiga ratus juta"}}
	results := Run(context.Background(), set, dir, []Provider{{Name: "fake", Transcriber: tr}}, time.Second)

	r := results[0]
	if tr.calls != 2 || tr.lang != "id" {
		t.Errorf("provider called %d times with %q; want the two real recordings, in the set's language", tr.calls, tr.lang)
	}
	if len(r.Scores) != 2 || len(r.Missing) != 1 || r.Missing[0] != "sold-40" || len(r.Failed) != 1 || r.Failed["sold-14"] == "" {
		t.Fatalf("result = %+v", r)
	}
	if !r.Scores[0].NumbersRight || r.Scores[1].NumbersRight {
		t.Errorf("scores = %+v; want 300 juta right and the 30m clip heard as 300 juta wrong", r.Scores)
	}
	report := Report(set, results, Pairs)
	for _, want := range []string{"| fake | fixed | 2 | 1 | 1 |", "1 / 2", "0 / 1", "**Missing recordings:** sold-40", "**Failed `sold-14`:**"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
}
