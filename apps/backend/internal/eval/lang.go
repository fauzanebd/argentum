package eval

import (
	"regexp"
	"strings"
)

// The agent's single most-repeated instruction is "reply in the language the
// user wrote in", and the failure mode it guards against — answering an
// English question in Indonesian — is one an LLM judge would catch but at
// the price of a second model call per case, plus a second thing that can be
// wrong. A stopword ratio catches it for free and is deterministic, which
// matters more in a regression harness than nuance does.
//
// Words chosen to be common in ordinary Indonesian business prose and absent
// from English. "data", "total" and "sales" are deliberately excluded: they
// appear in both languages and in this product's replies constantly.
var indonesianStopwords = map[string]bool{
	"yang": true, "dan": true, "untuk": true, "dari": true, "dengan": true,
	"pada": true, "adalah": true, "ini": true, "itu": true, "tidak": true,
	"ada": true, "dalam": true, "akan": true, "bisa": true, "saya": true,
	"anda": true, "kami": true, "atau": true, "juga": true, "sebesar": true,
	"berikut": true, "sebanyak": true, "penjualan": true, "bulan": true,
	"tahun": true, "terbesar": true, "tertinggi": true, "jumlah": true,
	"rata": true, "produk": true, "pelanggan": true, "pendapatan": true,
	"maaf": true, "hanya": true, "tentang": true, "bantu": true, "silakan": true,
}

// englishStopwords play the same role in the other direction. Without them a
// terse numeric reply ("Rp 3,86 Miliar") scores as neither language, and the
// case would fail for the wrong reason.
var englishStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "from": true, "with": true,
	"was": true, "were": true, "this": true, "that": true, "not": true,
	"your": true, "our": true, "you": true, "have": true, "has": true,
	"can": true, "could": true, "would": true, "there": true, "here": true,
	"about": true, "which": true, "please": true, "sorry": true, "help": true,
	"month": true, "year": true, "highest": true, "revenue": true,
	"customer": true, "customers": true, "product": true, "products": true,
}

var wordSplit = regexp.MustCompile(`[^\p{L}]+`)

// DetectLanguage returns "id", "en", or "" when the text carries too little
// signal to tell (a bare number, an empty reply).
func DetectLanguage(text string) string {
	words := wordSplit.Split(strings.ToLower(text), -1)
	var id, en int
	for _, w := range words {
		if indonesianStopwords[w] {
			id++
		}
		if englishStopwords[w] {
			en++
		}
	}
	switch {
	case id == 0 && en == 0:
		return ""
	case id > en:
		return "id"
	case en > id:
		return "en"
	default:
		return ""
	}
}
