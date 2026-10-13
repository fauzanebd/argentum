package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// TopupStepIDR is fixed, not configured: whole thousands keep the last three
// digits free for the matching code, so every request reads "Rp 150.<347>".
const TopupStepIDR = 1000

// topupMaxCeilingIDR bounds TOPUP_MAX_IDR. The credit is amount × 10¹⁰ ÷ rate
// in int64, and Rp 100.000.000 × 10¹⁰ is 10¹⁸ — under the 9.2 × 10¹⁸ int64
// ceiling with room to spare. A larger cap would be a credit computation that
// silently wraps.
const topupMaxCeilingIDR = 100_000_000

// TopupConfig is the parsed, validated top-up configuration.
type TopupConfig struct {
	// Payees is empty when TOPUP_PAYEES is unset, which is the off state.
	Payees            []domain.TopupPayee
	FXURL             string
	SpreadBP          int
	FXTimeout         time.Duration
	FXCacheFor        time.Duration
	FXMaxAge          time.Duration
	FallbackIDRPerUSD int64
	MinIDR            int64
	MaxIDR            int64
	PresetsIDR        []int64
}

// Enabled reports whether a top-up can be requested: payees are configured.
func (t TopupConfig) Enabled() bool { return len(t.Payees) > 0 }

// ErrTopupPayeesUnset is the off state rather than a mistake: a deployment
// that has not set TOPUP_PAYEES simply has no top-up.
var ErrTopupPayeesUnset = errors.New("TOPUP_PAYEES is not set")

// TopupSettings parses the RIZ-83 keys (R2). Any error means top-up is off;
// the caller logs it and carries on, because a misconfigured payment page is
// not a reason to refuse to boot the API that serves chat.
//
// Payee account numbers are never part of an error or a log line here: the
// owner asked for them to stay out of everywhere but the page that shows them.
func (c *Config) TopupSettings() (TopupConfig, error) {
	out := TopupConfig{
		FXURL:             strings.TrimSpace(c.TopupFXURL),
		FXTimeout:         time.Duration(c.TopupFXTimeoutMS) * time.Millisecond,
		FXCacheFor:        time.Duration(c.TopupFXCacheHours) * time.Hour,
		FXMaxAge:          time.Duration(c.TopupFXMaxAgeHours) * time.Hour,
		FallbackIDRPerUSD: int64(c.TopupIDRPerUSD),
		MinIDR:            int64(c.TopupMinIDR),
		MaxIDR:            int64(c.TopupMaxIDR),
	}
	if out.FXTimeout <= 0 {
		out.FXTimeout = 3 * time.Second
	}
	if out.FXCacheFor <= 0 {
		out.FXCacheFor = 24 * time.Hour
	}
	if out.FXMaxAge < out.FXCacheFor {
		out.FXMaxAge = out.FXCacheFor
	}

	if strings.TrimSpace(c.TopupPayeesJSON) == "" {
		return out, ErrTopupPayeesUnset
	}
	payees, err := parseTopupPayees(c.TopupPayeesJSON)
	if err != nil {
		return out, err
	}
	spread, err := parseSpreadBP(c.TopupFXSpreadPct)
	if err != nil {
		return out, err
	}
	out.SpreadBP = spread
	if out.FallbackIDRPerUSD <= 0 {
		return out, fmt.Errorf("TOPUP_IDR_PER_USD must be above zero, got %d", out.FallbackIDRPerUSD)
	}
	if out.MinIDR < TopupStepIDR || out.MinIDR%TopupStepIDR != 0 || out.MaxIDR%TopupStepIDR != 0 {
		return out, fmt.Errorf("TOPUP_MIN_IDR and TOPUP_MAX_IDR must be whole thousands of at least 1000, got %d and %d", out.MinIDR, out.MaxIDR)
	}
	if out.MinIDR > out.MaxIDR {
		return out, fmt.Errorf("TOPUP_MIN_IDR (%d) is above TOPUP_MAX_IDR (%d)", out.MinIDR, out.MaxIDR)
	}
	if out.MaxIDR > topupMaxCeilingIDR {
		return out, fmt.Errorf("TOPUP_MAX_IDR (%d) is above the %d ceiling", out.MaxIDR, topupMaxCeilingIDR)
	}
	out.PresetsIDR = parsePresets(c.TopupPresetsIDR, out.MinIDR, out.MaxIDR)
	out.Payees = payees
	return out, nil
}

func parseTopupPayees(raw string) ([]domain.TopupPayee, error) {
	var payees []domain.TopupPayee
	if err := json.Unmarshal([]byte(raw), &payees); err != nil {
		// Not %v of the input: a half-typed JSON list is the account numbers.
		return nil, fmt.Errorf("TOPUP_PAYEES is not a JSON list of {id, label, account_number, account_name}: %s", jsonErrorShape(err))
	}
	if len(payees) == 0 {
		return nil, errors.New("TOPUP_PAYEES is an empty list")
	}
	seen := map[string]bool{}
	for i, p := range payees {
		p.ID = strings.TrimSpace(p.ID)
		p.Label = strings.TrimSpace(p.Label)
		p.AccountNumber = strings.TrimSpace(p.AccountNumber)
		p.AccountName = strings.TrimSpace(p.AccountName)
		if p.ID == "" || p.Label == "" || p.AccountNumber == "" || p.AccountName == "" {
			return nil, fmt.Errorf("TOPUP_PAYEES entry %d is missing id, label, account_number or account_name", i+1)
		}
		if seen[p.ID] {
			return nil, fmt.Errorf("TOPUP_PAYEES has the id %q twice", p.ID)
		}
		seen[p.ID] = true
		payees[i] = p
	}
	return payees, nil
}

// jsonErrorShape describes a JSON error without quoting the input it came from.
func jsonErrorShape(err error) string {
	var syn *json.SyntaxError
	if errors.As(err, &syn) {
		return fmt.Sprintf("syntax error at byte %d", syn.Offset)
	}
	var typ *json.UnmarshalTypeError
	if errors.As(err, &typ) {
		return fmt.Sprintf("field %q has the wrong type", typ.Field)
	}
	return "unreadable"
}

// parseSpreadBP reads a percentage with at most two decimals into basis
// points, exactly: "5" is 500, "2.5" is 250, "0.75" is 75. More decimals than
// that is refused rather than rounded — a spread is a price, and a price that
// is not what was typed is a support conversation.
func parseSpreadBP(raw string) (int, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 500, nil
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || strings.ContainsAny(s, "eE/") {
		return 0, fmt.Errorf("TOPUP_FX_SPREAD_PCT %q is not a percentage", s)
	}
	bp := new(big.Rat).Mul(r, big.NewRat(100, 1))
	if !bp.IsInt() {
		return 0, fmt.Errorf("TOPUP_FX_SPREAD_PCT %q has more than two decimals", s)
	}
	n := bp.Num().Int64()
	if n < 0 || n > 5000 {
		return 0, fmt.Errorf("TOPUP_FX_SPREAD_PCT %q is outside 0–50", s)
	}
	return int(n), nil
}

// parsePresets keeps the quick-pick amounts that are whole thousands inside
// the range and drops the rest with a warning: a preset the server would
// refuse is a button that only ever produces an error.
func parsePresets(raw string, minIDR, maxIDR int64) []int64 {
	out := []int64{}
	for _, part := range splitCSV(raw) {
		n, err := strconv.ParseInt(part, 10, 64)
		if err != nil || n < minIDR || n > maxIDR || n%TopupStepIDR != 0 {
			logrus.WithField("preset", part).Warn("TOPUP_PRESETS_IDR entry is not a whole thousand between the minimum and the maximum; dropped")
			continue
		}
		out = append(out, n)
	}
	return out
}
