package format

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// This file is the formatting direction of the package: numbers and dates out
// as text. parse.go is the reading direction. They share the conventions
// declared at the top of parse.go, and a change to one is a change to both —
// if "Rp 3,86 Miliar" stops parsing back to 3.86e9, the eval comparator starts
// disagreeing with the document renderer about what the product said.

// Locale is a formatting convention, not a language. It decides separators,
// currency placement, magnitude words and month names. Two are supported
// because two are shipped: Indonesian tenants and everyone else.
type Locale string

const (
	LocaleEN Locale = "en"
	LocaleID Locale = "id"
)

// ParseLocale accepts "id", "id-ID", "in" (the legacy ISO code Java still
// emits) and anything else falls back to English rather than erroring: a
// document with slightly wrong thousands separators beats no document.
func ParseLocale(s string) Locale {
	switch strings.ToLower(strings.TrimSpace(strings.SplitN(s, "-", 2)[0])) {
	case "id", "in", "ind":
		return LocaleID
	default:
		return LocaleEN
	}
}

// LocaleForCurrency picks the convention a tenant most likely wants when they
// have not said. A rupiah report written with English separators looks wrong to
// the only people who will read it.
func LocaleForCurrency(code string) Locale {
	if strings.EqualFold(strings.TrimSpace(code), "IDR") {
		return LocaleID
	}
	return LocaleEN
}

func (l Locale) groupSep() string {
	if l == LocaleID {
		return "."
	}
	return ","
}

func (l Locale) decimalSep() string {
	if l == LocaleID {
		return ","
	}
	return "."
}

// Kind is what a cell holds, which is what decides how it is written and which
// edge it is aligned to. It is the `fmt` field of a spec column or cell.
type Kind string

const (
	KindText     Kind = "text"
	KindNumber   Kind = "number"
	KindCurrency Kind = "currency"
	KindPercent  Kind = "percent"
	KindDate     Kind = "date"
)

// ParseKind maps a spec string to a Kind, defaulting to text. An unknown fmt
// renders the value verbatim, which is the safe direction: a misspelt "curency"
// shows the number the caller passed, not an error page.
func ParseKind(s string) Kind {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "number", "num", "int", "float", "decimal":
		return KindNumber
	case "currency", "money", "amount":
		return KindCurrency
	case "percent", "percentage", "pct":
		return KindPercent
	case "date", "datetime", "time":
		return KindDate
	default:
		return KindText
	}
}

// Numeric reports whether values of this kind are numbers, and therefore
// right-aligned with a consistent number of decimals down a column. This is
// most of what separates a professional table from a dumped one.
func (k Kind) Numeric() bool {
	return k == KindNumber || k == KindCurrency || k == KindPercent
}

// AutoDecimals asks the formatter to choose: integers print without a decimal
// part, everything else gets two places. A table column overrides this with a
// single decided value so a column does not print 1,5 above 1,50.
const AutoDecimals = -1

// Options carries everything a value needs beyond its own kind.
type Options struct {
	Locale Locale

	// Currency is an ISO 4217 code ("IDR", "USD"). Empty means the renderer
	// had no company setting and no spec field, in which case currency values
	// print as bare numbers rather than guessing a symbol.
	Currency string

	// Decimals is the fixed number of decimal places, or AutoDecimals.
	Decimals int

	// Compact turns 3_863_405_700 into "3,86 Miliar". Off by default: a table
	// of exact figures must stay exact. KPI cards and chart axes turn it on.
	Compact bool

	// CompactThreshold is the magnitude at which Compact starts applying.
	// Zero means one million — below that, magnitude words read as evasion
	// ("0,4 Juta" instead of "400.000").
	CompactThreshold float64

	// ShortDate abbreviates the month: "1 Jan 2026" instead of
	// "1 Januari 2026". Tables turn it on and nothing else does — a date
	// column written out in full costs about 12mm of an A4 measure, which in
	// an eight-column table is taken from the customer name beside it.
	ShortDate bool
}

func (o Options) decimalsOr(v float64) int {
	if o.Decimals >= 0 {
		return o.Decimals
	}
	if v == math.Trunc(v) {
		return 0
	}
	return 2
}

// currencySymbols is what goes in front of the digits. A code with no symbol
// prints as the code itself ("SGD 1,200.00"), which is unambiguous and still
// parses back through Parse.
var currencySymbols = map[string]string{
	"IDR": "Rp",
	"USD": "$",
	"EUR": "€",
	"GBP": "£",
	"JPY": "¥",
	"SGD": "S$",
	"MYR": "RM",
	"AUD": "A$",
	"CNY": "¥",
}

// CurrencySymbol returns the prefix for an ISO 4217 code, or the code itself
// when there is no well-known symbol.
func CurrencySymbol(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return ""
	}
	if s, ok := currencySymbols[code]; ok {
		return s
	}
	return code
}

// currencyDecimals is the minor-unit count where it differs from 2. Rupiah has
// a legal subunit nobody has used since the 1990s; printing "Rp 1.234.567,00"
// in an Indonesian report marks the document as machine-generated.
var currencyDecimals = map[string]int{
	"IDR": 0,
	"JPY": 0,
	"KRW": 0,
	"VND": 0,
}

// Decimal writes a plain magnitude with locale grouping.
//
// Named Decimal rather than Number because Number is already the parse
// direction's result type — see parse.go. One package, two directions, and
// exactly one name each.
func Decimal(v float64, o Options) string {
	if !finite(v) {
		return "—"
	}
	if o.Compact && shouldCompact(v, o.CompactThreshold) {
		return Compact(v, o)
	}
	return group(strconv.FormatFloat(v, 'f', o.decimalsOr(v), 64), o.Locale)
}

// Currency writes a monetary amount: symbol, a hard space, then the digits.
// The space is not decorative — "Rp1.234" is harder to read at 9pt than
// "Rp 1.234", and it is what every Indonesian invoice does.
//
// Negatives print with a leading minus rather than accounting parentheses:
// parentheses are conventional in finance but they do not round-trip through
// Parse, and one comparator disagreeing with one renderer is the failure this
// package exists to prevent.
func Currency(v float64, o Options) string {
	if !finite(v) {
		return "—"
	}
	if o.Decimals == AutoDecimals {
		if d, ok := currencyDecimals[strings.ToUpper(strings.TrimSpace(o.Currency))]; ok {
			o.Decimals = d
		} else if o.Currency != "" {
			o.Decimals = 2
		}
	}
	digits := Decimal(math.Abs(v), o)
	sym := CurrencySymbol(o.Currency)
	sign := ""
	if v < 0 {
		sign = "-"
	}
	if sym == "" {
		return sign + digits
	}
	return sign + sym + symbolGap(sym) + digits
}

// symbolGap decides whether a space follows the currency symbol.
//
// The rule is what each convention actually does rather than one choice
// applied everywhere: a symbol that ends in a letter takes a space (Rp 1.234,
// RM 1.234, and the ISO-code fallback IDR 1.234), a glyph does not ($1,234,
// €1.234, S$1,234). "Rp1.234" and "$ 1,234" are both wrong, and both are
// immediately visible to the only readers who matter.
func symbolGap(sym string) string {
	if sym == "" {
		return ""
	}
	last := rune(sym[len(sym)-1])
	if last >= 'A' && last <= 'Z' || last >= 'a' && last <= 'z' {
		return " "
	}
	return ""
}

// Percent writes a share. The input is already in percent units: 12.5 means
// 12.5%, not 1250%. Every SQL query in this product computes it that way, and
// a renderer that silently multiplied by 100 would be indistinguishable from a
// query that forgot to.
func Percent(v float64, o Options) string {
	if !finite(v) {
		return "—"
	}
	if o.Decimals == AutoDecimals {
		// One decimal place is right for the percentages a report is usually
		// made of, and wrong for the ones below 1%: a damage rate of 0.42%
		// printed as "0.4%" has lost the two basis points the paragraph
		// underneath it is about.
		o.Decimals = 1
		if math.Abs(v) < 1 {
			o.Decimals = 2
		}
	}
	o.Compact = false
	return group(strconv.FormatFloat(v, 'f', o.decimalsOr(v), 64), o.Locale) + "%"
}

// Signed is Percent-style output for a delta: an explicit + on gains, because
// a KPI card showing "3,2%" leaves the reader guessing which way it moved.
func Signed(v float64, o Options) string {
	s := Percent(v, o)
	if v > 0 {
		return "+" + s
	}
	return s
}

// magnitudes are the compact steps, largest first. The Indonesian words are
// spelled out; English uses the single letters that parse.go reads back.
var magnitudes = []struct {
	value float64
	id    string
	en    string
}{
	{1e12, "Triliun", "T"},
	{1e9, "Miliar", "B"},
	{1e6, "Juta", "M"},
	{1e3, "Ribu", "K"},
}

// Compact writes a magnitude word or letter: "3,86 Miliar" / "3.86B".
//
// Indonesian puts a space before the word because it is a word; English does
// not, because a suffix letter jammed against the digits is the convention
// every dashboard uses. Both forms parse back to the same float.
//
// Options.Decimals is deliberately ignored: it describes the plain form, where
// rupiah takes none, and applying it here would render 3_863_405_700 as
// "Rp 4 Miliar". A compacted figure needs three significant digits or it stops
// being a figure, so the precision comes from the magnitude instead.
func Compact(v float64, o Options) string {
	if !finite(v) {
		return "—"
	}
	abs := math.Abs(v)
	for _, m := range magnitudes {
		if abs < m.value {
			continue
		}
		scaled := v / m.value
		decimals := 2
		if math.Abs(scaled) >= 100 {
			decimals = 0
		} else if math.Abs(scaled) >= 10 {
			decimals = 1
		}
		digits := group(strconv.FormatFloat(scaled, 'f', decimals, 64), o.Locale)
		if o.Locale == LocaleID {
			return digits + " " + m.id
		}
		return digits + m.en
	}
	plain := o
	plain.Compact = false
	return Decimal(v, plain)
}

func shouldCompact(v float64, threshold float64) bool {
	if threshold <= 0 {
		threshold = 1e6
	}
	return math.Abs(v) >= threshold
}

// monthNames is indexed by time.Month - 1.
var monthNames = map[Locale][]string{
	LocaleID: {
		"Januari", "Februari", "Maret", "April", "Mei", "Juni",
		"Juli", "Agustus", "September", "Oktober", "November", "Desember",
	},
	LocaleEN: {
		"January", "February", "March", "April", "May", "June",
		"July", "August", "September", "October", "November", "December",
	},
}

// shortMonthNames are the abbreviations each locale actually uses. Indonesian
// shortens Agustus to "Agu" and Oktober to "Okt", which a three-letter
// truncation of the long names would also produce — by luck, and only until
// someone adds a locale where it does not hold. Listing them is shorter than
// explaining that.
var shortMonthNames = map[Locale][]string{
	LocaleID: {
		"Jan", "Feb", "Mar", "Apr", "Mei", "Jun",
		"Jul", "Agu", "Sep", "Okt", "Nov", "Des",
	},
	LocaleEN: {
		"Jan", "Feb", "Mar", "Apr", "May", "Jun",
		"Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
	},
}

// Date writes "27 Juli 2026" / "27 July 2026", or "27 Jul 2026" when
// Options.ShortDate is set. Day-first in both, because the month-first
// American form is ambiguous to the majority of this product's readers and
// unambiguous to none of them.
func Date(t time.Time, o Options) string {
	names := monthNames[o.Locale]
	if o.ShortDate {
		names = shortMonthNames[o.Locale]
	}
	if names == nil {
		names = monthNames[LocaleEN]
	}
	return fmt.Sprintf("%d %s %d", t.Day(), names[int(t.Month())-1], t.Year())
}

// DateTime is Date plus a 24-hour clock. Used for the generated-at stamp in
// the footer, where the time of day is what tells two versions of the same
// report apart.
func DateTime(t time.Time, o Options) string {
	// Never abbreviated: this is the cover and footer stamp, where there is
	// room for the month and where a document's date is the thing a reader
	// checks first.
	o.ShortDate = false
	return Date(t, o) + " " + t.Format("15:04")
}

// dateLayouts are tried in order against a string cell. Everything a SQL
// driver, a JSON encoder or an LLM is likely to emit for a date.
var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"2006/01/02",
	"02/01/2006",
	"01/02/2006",
	"2006-01",
	"January 2, 2006",
	"2 January 2006",
}

// ParseTime reads a date out of a string cell. Returns false rather than a
// zero time so a caller can fall back to printing the string unchanged — an
// unparseable date is still information, and "1 Januari 1" is not.
func ParseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// Value formats one cell. This is the entry point the renderers use, and the
// reason the LLM no longer has to format anything itself.
//
// A string that arrives where a number was declared is parsed with this
// package's own reader before being reformatted, so "Rp 1.234.567" typed by
// the model comes out as "Rp 1.234.567" typed by the renderer — same
// separators as every other cell in the column, whatever the model felt like
// that turn. A string that will not parse is passed through verbatim rather
// than dropped: the reader can see a bad cell, but never a missing one.
func Value(v any, k Kind, o Options) string {
	if v == nil {
		return ""
	}
	if o.Locale == "" {
		o.Locale = LocaleEN
	}

	switch t := v.(type) {
	case time.Time:
		if k == KindDate || k == KindText {
			return Date(t, o)
		}
		return Date(t, o)
	case bool:
		return strconv.FormatBool(t)
	case string:
		return formatString(t, k, o)
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return t.String()
		}
		return formatFloat(f, k, o)
	case float64:
		return formatFloat(t, k, o)
	case float32:
		return formatFloat(float64(t), k, o)
	case int:
		return formatFloat(float64(t), k, o)
	case int32:
		return formatFloat(float64(t), k, o)
	case int64:
		return formatFloat(float64(t), k, o)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func formatFloat(f float64, k Kind, o Options) string {
	switch k {
	case KindCurrency:
		return Currency(f, o)
	case KindPercent:
		return Percent(f, o)
	case KindDate:
		// A numeric date is a Unix timestamp; nothing else in this product
		// produces one, and printing 1785283200 in a date column is worse
		// than guessing.
		return Date(time.Unix(int64(f), 0).UTC(), o)
	case KindNumber:
		return Decimal(f, o)
	default:
		return Decimal(f, o)
	}
}

func formatString(s string, k Kind, o Options) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	switch k {
	case KindText:
		return s
	case KindDate:
		if t, ok := ParseTime(trimmed); ok {
			return Date(t, o)
		}
		return s
	default:
		n, ok := Parse(trimmed)
		if !ok {
			return s
		}
		// A string that carried its own currency wins over the document
		// default: a table mixing IDR and USD columns is rare but real, and
		// relabelling one of them is a factual error, not a formatting one.
		if k == KindCurrency && n.Currency != "" {
			o.Currency = normalizeCurrency(n.Currency)
		}
		if k == KindPercent && !n.IsPercent {
			// The model wrote a bare number in a percent column. Trust the
			// column, not the cell.
			return Percent(n.Value, o)
		}
		return formatFloat(n.Value, k, o)
	}
}

// normalizeCurrency maps a symbol seen in text back to an ISO code so the
// renderer's own symbol table decides how it is written.
func normalizeCurrency(sym string) string {
	switch strings.ToUpper(strings.TrimSpace(sym)) {
	case "RP", "IDR":
		return "IDR"
	case "$", "US$", "USD":
		return "USD"
	case "€", "EUR":
		return "EUR"
	case "£", "GBP":
		return "GBP"
	case "S$", "SGD":
		return "SGD"
	case "RM", "MYR":
		return "MYR"
	case "¥":
		return "JPY"
	default:
		return strings.ToUpper(strings.TrimSpace(sym))
	}
}

// group inserts the locale's thousands separator into an already-rendered
// decimal string, and swaps '.' for the locale's decimal separator.
//
// Done by hand rather than with golang.org/x/text: this is twenty lines and no
// dependency, against a package whose CLDR tables would add megabytes to a
// worker image for two locales.
func group(s string, loc Locale) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")

	intPart, frac, _ := strings.Cut(s, ".")

	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	sep := loc.groupSep()
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteRune(c)
	}
	if frac != "" {
		b.WriteString(loc.decimalSep())
		b.WriteString(frac)
	}
	return b.String()
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// InferKind guesses a column's type from its values. It exists because the
// model omits `fmt` more often than it sets it, and an unformatted numeric
// column is the single most visible difference between a generated document
// and a designed one.
//
// The rules are deliberately unanimous rather than majority: every non-empty
// value must agree, or the column stays text. A column of order IDs that
// happen to be numeric is better left alone than right-aligned and grouped
// into "1.234.567".
func InferKind(values []any) Kind {
	var (
		seen      int
		numeric   int
		currency  int
		percent   int
		dates     int
		allInt    = true
		allYears  = true
		bigEnough bool
	)
	for _, v := range values {
		switch t := v.(type) {
		case nil:
			continue
		case time.Time:
			seen++
			dates++
			continue
		case string:
			s := strings.TrimSpace(t)
			if s == "" {
				continue
			}
			seen++
			if _, ok := ParseTime(s); ok {
				dates++
				continue
			}
			n, ok := Parse(s)
			if !ok {
				continue
			}
			numeric++
			if n.Currency != "" {
				currency++
			}
			if n.IsPercent {
				percent++
			}
			if n.Value != math.Trunc(n.Value) {
				allInt = false
			}
			if math.Abs(n.Value) >= 1000 {
				bigEnough = true
			}
			if !isYear(n.Value) {
				allYears = false
			}
		default:
			f, ok := toFloat(v)
			if !ok {
				continue
			}
			seen++
			numeric++
			if f != math.Trunc(f) {
				allInt = false
			}
			if math.Abs(f) >= 1000 {
				bigEnough = true
			}
			if !isYear(f) {
				allYears = false
			}
		}
	}
	if seen == 0 {
		return KindText
	}
	switch {
	case dates == seen:
		return KindDate
	case numeric == seen && currency == seen:
		return KindCurrency
	case numeric == seen && percent == seen:
		return KindPercent
	case numeric == seen:
		// An all-integer column with nothing over 999 is far more likely to be
		// an id, a year or a count of things than a measurement worth
		// grouping. Leaving it as text costs a right-align; getting it wrong
		// prints "2.026" where the year 2026 was meant — which is also why a
		// column of nothing but plausible years is left alone regardless of
		// magnitude.
		if allInt && (!bigEnough || allYears) {
			return KindText
		}
		return KindNumber
	default:
		return KindText
	}
}

// InferDecimals picks one decimal count for a whole column: the most any value
// needs, capped at two. Consistency down the column is the point — 1,5 above
// 1,50 above 2 is what a dumped table looks like.
func InferDecimals(values []any) int {
	for _, v := range values {
		f, ok := toFloat(v)
		if !ok {
			if s, isStr := v.(string); isStr {
				n, parsed := Parse(strings.TrimSpace(s))
				if !parsed {
					continue
				}
				f = n.Value
			} else {
				continue
			}
		}
		if f == math.Trunc(f) {
			continue
		}
		// Two is the cap: a report that needs three decimal places needs a
		// different column, not a wider one.
		return 2
	}
	return 0
}

// isYear reports whether v is a plausible calendar year. The range is wide
// enough for a founding date and narrow enough that a column of prices does
// not fall into it.
func isYear(v float64) bool {
	return v == math.Trunc(v) && v >= 1900 && v <= 2100
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
