package usage

import (
	"encoding/json"
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

var ranges = []RangeSpec{
	{ID: "24h", Window: 24 * time.Hour, Bucket: time.Hour},
	{ID: "7d", Window: 7 * 24 * time.Hour, Bucket: 24 * time.Hour},
	{ID: "30d", Window: 30 * 24 * time.Hour, Bucket: 24 * time.Hour},
	{ID: "90d", Window: 90 * 24 * time.Hour, Bucket: 24 * time.Hour},
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
// travels: a handful of rows per provider-model pair, never a session list
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

// EventRow is one durable event the aggregation walks, as the store hands it
// over: the three event types that can say anything about usage accounting —
// session.created and session.config_changed carry the model attribution,
// usage.updated carries the token counts.
type EventRow struct {
	SessionID string
	Harness   string
	Type      string
	Timestamp int64 // epoch ms
	Payload   json.RawMessage
}

// Aggregate folds the event rows of every session that used tokens in the
// window into one report.
//
// The semantics are per harness and this is where they matter:
//
//   - Claude's result message reports the turn's accounting total, and the
//     adapter re-emits the same numbers a moment later in its occupancy
//     report. Counting both would double every turn, so consecutive usage
//     events with identical token categories collapse into one.
//   - Codex's token totals are cumulative within a thread: each event says
//     "this much so far", so only the positive delta since the previous
//     event is usage. A reset (totals going backwards) means the thread
//     started counting again, and the new baseline is charged as-is.
//
// Events older than the window are still walked: a codex session whose last
// pre-window event was days ago still needs that baseline to delta against,
// and a model switch before the window still tells the walk which model an
// in-window event ran on.
func Aggregate(rows []EventRow, spec RangeSpec, now time.Time) Report {
	rep := Report{
		Range:        spec.ID,
		From:         now.Add(-spec.Window).UnixMilli(),
		To:           now.UnixMilli(),
		BucketMs:     spec.Bucket.Milliseconds(),
		PriceVersion: PriceVersion,
		Rows:         []Row{},
	}

	type cellKey struct {
		start    int64
		provider string
		model    string
	}
	cells := map[cellKey]*Row{}

	// Per-session walk state.
	var session string
	var model string
	var harness string
	// lastUsage is the previous usage.updated payload of the session being
	// walked, whatever it counted: the claude de-duplication and the codex
	// delta both need it.
	var lastUsage Counts
	haveLastUsage := false

	record := func(ts int64, c Counts) {
		if ts < rep.From {
			return
		}
		key := cellKey{start: bucketStart(ts, rep.BucketMs), provider: harness, model: model}
		cell, ok := cells[key]
		if !ok {
			cell = &Row{Start: key.start, Provider: key.provider, Model: key.model}
			cells[key] = cell
		}
		cell.Totals.addTokens(c)
		cell.Totals.addPriced(Price(key.model, c))
		rep.Totals.addTokens(c)
		rep.Totals.addPriced(Price(key.model, c))
	}

	for _, r := range rows {
		if r.SessionID != session {
			session = r.SessionID
			harness = r.Harness
			model = ""
			lastUsage, haveLastUsage = Counts{}, false
		}
		switch r.Type {
		case "session.created":
			var p struct {
				Model string `json:"model"`
			}
			_ = json.Unmarshal(r.Payload, &p)
			if p.Model != "" {
				model = p.Model
			}
		case "session.config_changed":
			var p struct {
				Model string `json:"model"`
			}
			_ = json.Unmarshal(r.Payload, &p)
			if p.Model != "" {
				model = p.Model
			}
		case "usage.updated":
			var p struct {
				Input      int64 `json:"input"`
				Output     int64 `json:"output"`
				CacheRead  int64 `json:"cacheRead"`
				CacheWrite int64 `json:"cacheWrite"`
				Accounting bool  `json:"accounting"`
			}
			if err := json.Unmarshal(r.Payload, &p); err != nil {
				continue
			}
			cur := Counts{Input: p.Input, Output: p.Output, CacheRead: p.CacheRead, CacheWrite: p.CacheWrite}
			counts := accountingDelta(harness, cur, lastUsage, haveLastUsage, p.Accounting)
			lastUsage, haveLastUsage = cur, true
			if counts == (Counts{}) {
				continue
			}
			record(r.Timestamp, counts)
		}
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

// accountingDelta turns one usage.updated payload into the usage it
// represents, applying the harness's own semantics.
func accountingDelta(harness string, cur, prev Counts, havePrev bool, accounting bool) Counts {
	if harness == "codex" {
		if !havePrev {
			return cur
		}
		// Cumulative totals: only the growth since the previous reading is
		// new usage. A reading below the previous one means the thread reset
		// its counter — the current total is fresh usage, not a negative.
		if cur.Input >= prev.Input && cur.Output >= prev.Output && cur.CacheRead >= prev.CacheRead && cur.CacheWrite >= prev.CacheWrite {
			return Counts{
				Input:      cur.Input - prev.Input,
				Output:     cur.Output - prev.Output,
				CacheRead:  cur.CacheRead - prev.CacheRead,
				CacheWrite: cur.CacheWrite - prev.CacheWrite,
			}
		}
		return cur
	}
	// The accounting flag is the source's own word for "this is fresh
	// accounting, count it" — and, absent, for "this restates the previous
	// reading, skip it".
	if accounting {
		return cur
	}
	if !havePrev {
		return cur
	}
	// Events recorded before the flag existed: consecutive identical counts
	// are the occupancy report restating the turn result, not a second turn.
	if cur == prev {
		return Counts{}
	}
	return cur
}

// bucketStart floors a timestamp to its bucket boundary. Epoch arithmetic, so
// buckets are timezone-independent: the client labels them in local time.
func bucketStart(ts, bucketMs int64) int64 {
	if bucketMs <= 0 {
		return ts
	}
	return ts - ts%bucketMs
}
