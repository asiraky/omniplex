package usage

import "testing"

func TestPriceKnownModel(t *testing.T) {
	// 1M input, 1M output on Opus 5: $5 + $25.
	p := Price("claude-opus-5", Counts{Input: 1_000_000, Output: 1_000_000})
	if p.CostUSD != 30 {
		t.Fatalf("cost = %v, want 30", p.CostUSD)
	}
	if p.Unpriced != 0 {
		t.Fatalf("unpriced = %v, want 0", p.Unpriced)
	}
}

func TestPriceCacheCategories(t *testing.T) {
	// Cache read is discounted, cache write is premium: 1M of each on Sonnet 5
	// is $0.20 + $2.50, not $2 + $2 and not free.
	p := Price("claude-sonnet-5", Counts{CacheRead: 1_000_000, CacheWrite: 1_000_000})
	if p.CostUSD != 2.7 {
		t.Fatalf("cost = %v, want 2.7", p.CostUSD)
	}
}

func TestPriceUnpublishedCacheWriteIsUnpricedNotFree(t *testing.T) {
	// OpenAI publishes no cache-write rate. Those tokens must be excluded from
	// the total and counted as unpriced, not silently priced as free.
	p := Price("gpt-5.2-codex", Counts{Input: 1_000_000, Output: 1_000_000, CacheWrite: 500_000})
	if p.CostUSD != 15.75 { // 1.75 + 14
		t.Fatalf("cost = %v, want 15.75", p.CostUSD)
	}
	if p.Unpriced != 500_000 {
		t.Fatalf("unpriced = %v, want 500000", p.Unpriced)
	}
}

func TestLookupNormalisation(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"claude-opus-5", true},
		{"claude-opus-5[1m]", true},
		{"CLAUDE-OPUS-5", true},
		{"anthropic/claude-opus-5", true},
		{"gpt-5.2-codex-2025-12-11", true},
		{"gpt-5.1-codex-max", true},
		{"", false},
		{"default", false},
		{"opus", false},   // family alias: ambiguous across generations
		{"sonnet", false}, // ditto
		{"claude-opus-9", false},
		{"some-unknown-model", false},
	}
	for _, c := range cases {
		if _, ok := LookupRates(c.model); ok != c.want {
			t.Errorf("LookupRates(%q) found = %v, want %v", c.model, ok, c.want)
		}
	}
}

func TestPriceUnknownModelAllUnpriced(t *testing.T) {
	p := Price("claude-opus-9", Counts{Input: 100, Output: 200, CacheRead: 300, CacheWrite: 400})
	if p.CostUSD != 0 {
		t.Fatalf("cost = %v, want 0", p.CostUSD)
	}
	if p.Unpriced != 1000 {
		t.Fatalf("unpriced = %v, want 1000", p.Unpriced)
	}
}
