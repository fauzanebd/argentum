package evalspeech

// Pairs are the set's one-syllable neighbours: two lines that differ by a
// number a provider could mishear as the other. The report counts a pair right
// only when both halves are, because a provider that writes "tiga ratus juta"
// for both has heard neither.
var Pairs = [][2]string{
	{"revenue-300m", "revenue-30m"},
	{"sold-14", "sold-40"},
}
