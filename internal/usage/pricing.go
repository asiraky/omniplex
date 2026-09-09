// Package usage holds the account-level analytics: what work cost through
// the API, over time, from the durable event log. It is deliberately
// server-side — the aggregation ships small pre-bucketed rows to a client,
// never a session history.
package usage

import (
	"regexp"
	"strings"
)

// PriceVersion stamps every report. It names the pricing catalogue the
// figures were calculated against, so a historical answer says which prices
// it used rather than silently moving when a provider updates them.
const PriceVersion = "2026-04"

// Rates is one model's published API pricing, in USD per million tokens.
// A nil category means the provider publishes no price for it: those tokens
// are counted as unpriced and kept out of the dollar total rather than
// guessed at (a missing cache-write rate is not "free", and pricing cached
// input at full input rates is a guess dressed up as precision).
type Rates struct {
	Input      *float64
	Output     *float64
	CacheRead  *float64
	CacheWrite *float64
}

func rate(v float64) *float64 { return &v }

// catalog is the pricing table, keyed by the model id the harness records.
// Only per-million rates the provider actually publishes appear here; when a
// provider has no cache-write tier (OpenAI), the pointer stays nil and any
// cache-write tokens for that model report as unpriced.
var catalog = map[string]Rates{
	// Anthropic, api.anthropic.com pricing.
	"claude-opus-5":     {rate(5), rate(25), rate(0.5), rate(6.25)},
	"claude-opus-4-8":   {rate(5), rate(25), rate(0.5), rate(6.25)},
	"claude-opus-4-7":   {rate(5), rate(25), rate(0.5), rate(6.25)},
	"claude-opus-4-6":   {rate(5), rate(25), rate(0.5), rate(6.25)},
	"claude-opus-4-5":   {rate(5), rate(25), rate(0.5), rate(6.25)},
	"claude-opus-4-1":   {rate(15), rate(75), rate(1.5), rate(18.75)},
	"claude-sonnet-5":   {rate(2), rate(10), rate(0.2), rate(2.5)},
	"claude-sonnet-4-6": {rate(3), rate(15), rate(0.3), rate(3.75)},
	"claude-sonnet-4-5": {rate(3), rate(15), rate(0.3), rate(3.75)},
	"claude-fable-5":    {rate(10), rate(50), rate(1), rate(12.5)},
	"claude-haiku-4-5":  {rate(1), rate(5), rate(0.1), rate(1.25)},

	// OpenAI, for Codex sessions. Cached input is published; cache writes are
	// not, so those stay nil.
	"gpt-5.6-sol":        {rate(4), rate(20), rate(0.4), nil},
	"gpt-5.6":            {rate(4), rate(20), rate(0.4), nil},
	"gpt-5.6-terra":      {rate(2), rate(12), rate(0.2), nil},
	"gpt-5.6-luna":       {rate(0.2), rate(1.2), rate(0.02), nil},
	"gpt-5.6-cyber":      {rate(12.5), rate(75), rate(1.25), nil},
	"gpt-5.5":            {rate(5), rate(30), rate(0.5), nil},
	"gpt-5.4":            {rate(2.5), rate(15), rate(0.25), nil},
	"gpt-5.4-mini":       {rate(0.75), rate(4.5), rate(0.075), nil},
	"gpt-5.3-codex":      {rate(1.75), rate(14), rate(0.175), nil},
	"gpt-5.3-chat":       {rate(1.75), rate(14), rate(0.175), nil},
	"gpt-5.2":            {rate(1.75), rate(14), rate(0.175), nil},
	"gpt-5.2-codex":      {rate(1.75), rate(14), rate(0.175), nil},
	"gpt-5.1":            {rate(1.25), rate(10), rate(0.125), nil},
	"gpt-5.1-codex":      {rate(1.25), rate(10), rate(0.125), nil},
	"gpt-5.1-codex-max":  {rate(1.25), rate(10), rate(0.125), nil},
	"gpt-5.1-codex-mini": {rate(0.25), rate(2), rate(0.025), nil},
	"gpt-5":              {rate(1.25), rate(10), rate(0.125), nil},
	"gpt-5-codex":        {rate(1.25), rate(10), rate(0.125), nil},
	"gpt-5-mini":         {rate(0.25), rate(2), rate(0.025), nil},
	"gpt-5-nano":         {rate(0.05), rate(0.4), rate(0.005), nil},
	"codex-mini-latest":  {rate(1.5), rate(6), rate(0.375), nil},
}

// unpriceable are model names that must never match a rate: bare family
// aliases ("opus") are ambiguous across generations, and an empty model means
// the harness picked one we never learned. Reporting those as unpriced is
// honest; pricing them at a guess is not.
var unpriceable = map[string]bool{
	"":          true,
	"default":   true,
	"opus":      true,
	"sonnet":    true,
	"fable":     true,
	"haiku":     true,
	"synthetic": true,
}

// dateSuffix matches a trailing dated release like "gpt-5.2-codex-2025-12-11",
// which prices the same as the undated id.
var dateSuffix = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)

// LookupRates finds a model's published pricing. The id is normalised the way
// harnesses record it: lowercase, the "[1m]" context tag dropped, any
// provider prefix ("anthropic/", "openai/") discarded, and a dated release
// suffix retried without the date.
func LookupRates(model string) (Rates, bool) {
	key := strings.ToLower(strings.TrimSpace(model))
	// Context-window tag: "claude-opus-5[1m]" prices at the base tier.
	if i := strings.LastIndex(key, "["); i >= 0 && strings.HasSuffix(key, "]") {
		key = key[:i]
	}
	// A provider-qualified id prices at its bare name.
	if i := strings.LastIndex(key, "/"); i >= 0 {
		key = key[i+1:]
	}
	if unpriceable[key] {
		return Rates{}, false
	}
	if r, ok := catalog[key]; ok {
		return r, true
	}
	if trimmed := dateSuffix.ReplaceAllString(key, ""); trimmed != key {
		r, ok := catalog[trimmed]
		return r, ok
	}
	return Rates{}, false
}

// Counts is one usage record's token categories — the four every provider
// reports, in the terms of the canonical usage payload.
type Counts struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
}

// Priced is the cost arithmetic over one record.
type Priced struct {
	// CostUSD is the value of the priced token categories.
	CostUSD float64
	// Unpriced is the token count of categories with no published rate. They
	// are excluded from CostUSD on purpose and surfaced separately, so a
	// missing price reads as "not counted" rather than "free".
	Unpriced int64
}

// Price calculates API-equivalent cost from token counts. A category with a
// published rate contributes its share; a category without one contributes
// its tokens to Unpriced and nothing to the cost.
func Price(model string, c Counts) Priced {
	r, ok := LookupRates(model)
	if !ok {
		return Priced{Unpriced: c.Input + c.Output + c.CacheRead + c.CacheWrite}
	}
	var p Priced
	charge := func(tokens int64, perMillion *float64) {
		if tokens == 0 {
			return
		}
		if perMillion == nil {
			p.Unpriced += tokens
			return
		}
		p.CostUSD += float64(tokens) * *perMillion / 1_000_000
	}
	charge(c.Input, r.Input)
	charge(c.Output, r.Output)
	charge(c.CacheRead, r.CacheRead)
	charge(c.CacheWrite, r.CacheWrite)
	return p
}
