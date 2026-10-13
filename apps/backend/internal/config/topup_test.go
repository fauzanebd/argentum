package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// RIZ-83 R2. Placeholder accounts only — the real ones are deploy config.
const testPayees = `[{"id":"bri","label":"BRI","account_number":"0000000000","account_name":"Payee Name"},
{"id":"gopay","label":"GoPay","account_number":"081200000000","account_name":"Payee Name"}]`

func topupConfig() *Config {
	return &Config{
		TopupPayeesJSON:    testPayees,
		TopupFXURL:         "https://api.frankfurter.app/latest?from=USD&to=IDR",
		TopupFXSpreadPct:   "5",
		TopupFXTimeoutMS:   3000,
		TopupFXCacheHours:  24,
		TopupFXMaxAgeHours: 72,
		TopupIDRPerUSD:     18000,
		TopupMinIDR:        20000,
		TopupMaxIDR:        5000000,
		TopupPresetsIDR:    "50000,100000,250000",
	}
}

func TestTopupSettingsDefaults(t *testing.T) {
	tc, err := topupConfig().TopupSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !tc.Enabled() || len(tc.Payees) != 2 || tc.Payees[1].Label != "GoPay" {
		t.Errorf("payees: %+v", tc.Payees)
	}
	if tc.SpreadBP != 500 || tc.FallbackIDRPerUSD != 18000 || tc.MinIDR != 20000 || tc.MaxIDR != 5000000 {
		t.Errorf("numbers: %+v", tc)
	}
	if tc.FXTimeout != 3*time.Second || tc.FXCacheFor != 24*time.Hour || tc.FXMaxAge != 72*time.Hour {
		t.Errorf("durations: %v %v %v", tc.FXTimeout, tc.FXCacheFor, tc.FXMaxAge)
	}
	if !reflect.DeepEqual(tc.PresetsIDR, []int64{50000, 100000, 250000}) {
		t.Errorf("presets: %v", tc.PresetsIDR)
	}
}

// Unset is the off state, distinguishable from a mistake.
func TestTopupSettingsUnsetIsOff(t *testing.T) {
	c := topupConfig()
	c.TopupPayeesJSON = ""
	tc, err := c.TopupSettings()
	if !errors.Is(err, ErrTopupPayeesUnset) || tc.Enabled() {
		t.Errorf("unset: %v, enabled %v", err, tc.Enabled())
	}
}

func TestTopupSettingsRefusals(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*Config)
	}{
		{"not json", func(c *Config) { c.TopupPayeesJSON = "not json" }},
		{"truncated json", func(c *Config) { c.TopupPayeesJSON = `[{"id":"bri","account_number":"0000000000"` }},
		{"empty list", func(c *Config) { c.TopupPayeesJSON = `[]` }},
		{"missing name", func(c *Config) {
			c.TopupPayeesJSON = `[{"id":"bri","label":"BRI","account_number":"0000000000"}]`
		}},
		{"duplicate id", func(c *Config) {
			c.TopupPayeesJSON = `[{"id":"bri","label":"BRI","account_number":"1","account_name":"A"},{"id":"bri","label":"BRI 2","account_number":"2","account_name":"A"}]`
		}},
		{"spread above 50", func(c *Config) { c.TopupFXSpreadPct = "51" }},
		{"negative spread", func(c *Config) { c.TopupFXSpreadPct = "-1" }},
		{"spread with three decimals", func(c *Config) { c.TopupFXSpreadPct = "5.125" }},
		{"spread as a word", func(c *Config) { c.TopupFXSpreadPct = "five" }},
		{"fallback zero", func(c *Config) { c.TopupIDRPerUSD = 0 }},
		{"fallback negative", func(c *Config) { c.TopupIDRPerUSD = -18000 }},
		{"min above max", func(c *Config) { c.TopupMinIDR, c.TopupMaxIDR = 100000, 50000 }},
		{"min not a thousand", func(c *Config) { c.TopupMinIDR = 20500 }},
		{"max above the int64 ceiling", func(c *Config) { c.TopupMaxIDR = 200_000_000 }},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := topupConfig()
			c.edit(cfg)
			_, err := cfg.TopupSettings()
			if err == nil || errors.Is(err, ErrTopupPayeesUnset) {
				t.Fatalf("accepted: %v", err)
			}
			// An error line is a log line: it must not quote an account number.
			if strings.Contains(err.Error(), "0000000000") {
				t.Errorf("the error quotes an account number: %v", err)
			}
		})
	}
}

func TestTopupSpreadIsParsedExactly(t *testing.T) {
	for in, want := range map[string]int{"5": 500, "2.5": 250, "0.75": 75, "0": 0, "50": 5000, " 10 ": 1000, "": 500} {
		got, err := parseSpreadBP(in)
		if err != nil || got != want {
			t.Errorf("parseSpreadBP(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
}

// A preset the server would refuse is dropped, with the rest kept in order.
func TestTopupPresetsOutsideTheRangeAreDropped(t *testing.T) {
	c := topupConfig()
	c.TopupPresetsIDR = "15000, 50000,100500,abc,250000,9000000"
	tc, err := c.TopupSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tc.PresetsIDR, []int64{50000, 250000}) {
		t.Errorf("presets: %v", tc.PresetsIDR)
	}
}

func TestLowerAllOperatorEmails(t *testing.T) {
	if got := lowerAll(splitCSV("Owner@Example.com, second@example.COM")); !reflect.DeepEqual(got, []string{"owner@example.com", "second@example.com"}) {
		t.Errorf("got %v", got)
	}
}
