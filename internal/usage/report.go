package usage

import (
	"fmt"
	"sort"
	"time"
)

// RangeSpec is one selectable window: how far back it reaches and how wide
// its buckets are. Past 24 hours is hourly; longer ranges are daily. The ids
// are the wire vocabulary the client sends back.
type RangeSpec struct {
	ID     string
	Window time.Duration
	Bucket time.Duration
}

// MaxWindow is the widest range's reach: no report reads further back.
const MaxWindow = 90 * 24 * time.Hour

var ranges = []RangeSpec{
	{ID: "24h", Window: 24 * time.Hour, Bucket: time.Hour},
	{ID: "7d", Window: 7 * 24 * time.Hour, Bucket: 24 * time.Hour},
	{ID: "30d", Window: 30 * 24 * time.Hour, Bucket: 24 * time.Hour},
	{ID: "90d", Window: MaxWindow, Bucket: 24 * time.Hour},
}

// RangeSpec resolves a range id. Unknown ids are refused rather than
// defaulted: a made-up window would silently answer a question nobody asked.
func RangeFor(id string) (RangeSpec, error) {
	for _, r := range ranges {
		if r.ID == id {
			return r, nil
		}
	}
	return RangeSpec{}, fmt.Errorf("unknown usage range %q", id)
}

// Totals is the token and cost arithmetic over some usage. It is embedded in
// each bucket row and summed for the headline.
type Totals struct {
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cacheRead"`
	CacheWrite int64   `json:"cacheWrite"`
	Cost       float64 `json:"cost"`
	// Unpriced counts tokens excluded from Cost because their model or
	// category had no published price. Zero means the figure is complete.
	Unpriced int64 `json:"unpriced"`
}

func (t *Totals) addTokens(c Counts) {
	t.Input += c.Input
	t.Output += c.Output
	t.CacheRead += c.CacheRead
	t.CacheWrite += c.CacheWrite
}

func (t *Totals) addPriced(p Priced) {
	t.Cost += p.CostUSD
	t.Unpriced += p.Unpriced
}

// Tokens is the record's total token count, for ranking and tooltips.
func (t Totals) Tokens() int64 {
	return t.Input + t.Output + t.CacheRead + t.CacheWrite
}

// Row is one aggregated cell: a bucket of time, a provider, a model. Only
// cells with usage are sent; the client knows From/BucketMs/To and renders
// the gaps as zero, so an idle hour stays an hour wide rather than
// disappearing and compressing the axis.
type Row struct {
	Start    int64  `json:"start"` // bucket start, epoch ms
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Totals   Totals `json:"totals"`
}

// Report is the whole answer for one range. It is the bounded aggregate that
// travels: a handful of rows per provider-model pair, never a thread list
// and never raw events.
type Report struct {
	Range        string `json:"range"`
	From         int64  `json:"from"` // epoch ms, inclusive
	To           int64  `json:"to"`   // epoch ms, exclusive
	BucketMs     int64  `json:"bucketMs"`
	PriceVersion string `json:"priceVersion"`
	Rows         []Row  `json:"rows"`
	Totals       Totals `json:"totals"`
}

// Record is one billed model response, as a harness wrote it to its own
// transcript.
type Record struct {
	Timestamp int64 // epoch ms
	Provider  string
	Model     string
	Counts    Counts
	// Cost is the harness's own figure for this response, used in place of
	// the catalogue when HasCost is set.
	Cost    float64
	HasCost bool
	// Key identifies the response across lines and files. Claude writes one
	// line per content block, each repeating the response's usage, and copies
	// lines forward on resume and fork; records sharing a key are one
	// response. Empty means the record is unique by construction.
	Key string
}

// Build folds transcript records into one report for the window ending now.
//
// Records sharing a key merge by taking each category's largest reading:
// the lines Claude writes while a response is still streaming carry a
// partial output count, so the first line seen is not the response's usage
// — the largest is.
func Build(records []Record, spec RangeSpec, now time.Time) Report {
	rep := Report{
		Range:        spec.ID,
		From:         now.Add(-spec.Window).UnixMilli(),
		To:           now.UnixMilli(),
		BucketMs:     spec.Bucket.Milliseconds(),
		PriceVersion: PriceVersion,
		Rows:         []Row{},
	}

	merged := make([]Record, 0, len(records))
	byKey := map[string]int{}
	for _, r := range records {
		if r.Timestamp < rep.From || r.Timestamp > rep.To {
			continue
		}
		if r.Key == "" {
			merged = append(merged, r)
			continue
		}
		i, ok := byKey[r.Key]
		if !ok {
			byKey[r.Key] = len(merged)
			merged = append(merged, r)
			continue
		}
		m := &merged[i]
		m.Timestamp = min(m.Timestamp, r.Timestamp)
		m.Counts = Counts{
			Input:        max(m.Counts.Input, r.Counts.Input),
			Output:       max(m.Counts.Output, r.Counts.Output),
			CacheRead:    max(m.Counts.CacheRead, r.Counts.CacheRead),
			CacheWrite:   max(m.Counts.CacheWrite, r.Counts.CacheWrite),
			CacheWrite1h: max(m.Counts.CacheWrite1h, r.Counts.CacheWrite1h),
		}
		if r.HasCost {
			m.Cost, m.HasCost = max(m.Cost, r.Cost), true
		}
	}

	type cellKey struct {
		start    int64
		provider string
		model    string
	}
	cells := map[cellKey]*Row{}
	for _, r := range merged {
		var priced Priced
		if r.HasCost {
			priced = Priced{CostUSD: r.Cost}
		} else {
			priced = Price(r.Model, r.Counts)
		}
		key := cellKey{start: bucketStart(r.Timestamp, rep.BucketMs), provider: r.Provider, model: r.Model}
		cell, ok := cells[key]
		if !ok {
			cell = &Row{Start: key.start, Provider: key.provider, Model: key.model}
			cells[key] = cell
		}
		cell.Totals.addTokens(r.Counts)
		cell.Totals.addPriced(priced)
		rep.Totals.addTokens(r.Counts)
		rep.Totals.addPriced(priced)
	}

	keys := make([]cellKey, 0, len(cells))
	for k := range cells {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.start != b.start {
			return a.start < b.start
		}
		if a.provider != b.provider {
			return a.provider < b.provider
		}
		return a.model < b.model
	})
	for _, k := range keys {
		rep.Rows = append(rep.Rows, *cells[k])
	}
	return rep
}

// bucketStart floors a timestamp to its bucket boundary. Epoch arithmetic, so
// buckets are timezone-independent: the client labels them in local time.
func bucketStart(ts, bucketMs int64) int64 {
	if bucketMs <= 0 {
		return ts
	}
	return ts - ts%bucketMs
}
