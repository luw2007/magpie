package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

const benchmarkURL = "https://api.codexradar.com/api/v1/intelligence-efficiency?benchmark=deep-swe"
const benchmarkTTL = 15 * time.Minute
const benchmarkFailureTTL = time.Minute

type benchmarkPoint struct {
	Model          string  `json:"model"`
	Effort         string  `json:"effort"`
	Total          int     `json:"total"`
	IQ             float64 `json:"iq"`
	AverageMinutes float64 `json:"average_minutes"`
}

// A missing numeric field is malformed evidence, not a measured zero.
func (p *benchmarkPoint) UnmarshalJSON(data []byte) error {
	var row struct {
		Model          string   `json:"model"`
		Effort         string   `json:"effort"`
		Total          *int     `json:"total"`
		IQ             *float64 `json:"iq"`
		AverageMinutes *float64 `json:"average_minutes"`
	}
	if err := json.Unmarshal(data, &row); err != nil {
		return err
	}
	if row.Model == "" || row.Effort == "" || row.Total == nil {
		return errors.New("incomplete benchmark point")
	}
	if *row.Total == 0 && (row.IQ == nil || row.AverageMinutes == nil) {
		*p = benchmarkPoint{Model: row.Model, Effort: row.Effort}
		return nil
	}
	if row.IQ == nil || row.AverageMinutes == nil {
		return errors.New("incomplete benchmark point")
	}
	*p = benchmarkPoint{row.Model, row.Effort, *row.Total, *row.IQ, *row.AverageMinutes}
	return nil
}

type benchmarkKey struct{ model, effort string }

type benchmarkSnapshot struct {
	mu      sync.Mutex
	client  *http.Client
	url     string
	expires time.Time
	points  map[benchmarkKey]benchmarkPoint
}

var deepSWE = &benchmarkSnapshot{client: &http.Client{Timeout: time.Second}, url: benchmarkURL}

// get returns nil on network or data errors. A short negative cache prevents a
// broken endpoint from delaying each request; the next refresh can recover.
func (b *benchmarkSnapshot) get() map[benchmarkKey]benchmarkPoint {
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Now().Before(b.expires) {
		return b.points
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.url, nil)
	if err == nil {
		var res *http.Response
		res, err = b.client.Do(req)
		if err == nil {
			defer res.Body.Close()
			if res.StatusCode != http.StatusOK {
				err = errors.New("benchmark endpoint returned non-200 status")
			} else {
				var doc struct {
					Points []benchmarkPoint `json:"points"`
				}
				decoder := json.NewDecoder(http.MaxBytesReader(nil, res.Body, 2<<20))
				if err = decoder.Decode(&doc); err == nil && len(doc.Points) > 0 {
					var extra any
					if e := decoder.Decode(&extra); e != io.EOF {
						err = errors.New("trailing benchmark data")
					}
					points := make(map[benchmarkKey]benchmarkPoint, len(doc.Points))
					for _, p := range doc.Points {
						if p.Model == "" || p.Effort == "" || p.Total == 0 {
							continue // CodexRadar includes rows awaiting samples
						}
						if p.Total < 0 || math.IsNaN(p.IQ) || math.IsInf(p.IQ, 0) || math.IsNaN(p.AverageMinutes) || math.IsInf(p.AverageMinutes, 0) || p.AverageMinutes <= 0 {
							err = errors.New("invalid benchmark point")
							break
						}
						key := benchmarkKey{strings.ToLower(p.Model), strings.ToLower(p.Effort)}
						if _, exists := points[key]; exists {
							err = errors.New("duplicate benchmark point")
							break
						}
						points[key] = p
					}
					if err == nil {
						b.points = points
						b.expires = time.Now().Add(benchmarkTTL)
						return b.points
					}
				}
			}
		}
	}
	b.points = nil // never sort using a stale snapshot after a failed refresh
	b.expires = time.Now().Add(benchmarkFailureTTL)
	return nil
}

// benchmarkModel removes only a provider prefix. A suffix such as "-high"
// may be part of the vendor's real model ID (Gemini), so it must stay intact.
func benchmarkModel(model string) string {
	model = strings.ToLower(model)
	if i := strings.LastIndexByte(model, '/'); i >= 0 {
		model = model[i+1:]
	}
	return model
}

func benchmarkEffort(c candidate, fallback string) string {
	e := fallback
	if c.effort != "" {
		e = c.effort
	}
	if e == "" {
		return ""
	}
	levels := c.p.Efforts(c.model)
	e = fitEffort(e, levels)
	if c.effort == "" && len(levels) == 0 && e == "xhigh" {
		return "high"
	}
	return e
}

// benchmarkOrder prioritizes qualified model+effort rows by average minutes.
// Missing effort has no reliable comparison and keeps configuration order.
func benchmarkOrder(cs []candidate, effort string, points map[benchmarkKey]benchmarkPoint) {
	if points == nil {
		return
	}
	qualified := func(c candidate) (float64, bool) {
		e := benchmarkEffort(c, effort)
		if e == "" {
			return 0, false
		}
		p, ok := points[benchmarkKey{benchmarkModel(c.model), e}]
		return p.AverageMinutes, ok && p.Total >= 20 && p.IQ >= 80
	}
	slices.SortStableFunc(cs, func(a, b candidate) int {
		am, aok := qualified(a)
		bm, bok := qualified(b)
		if aok != bok {
			if aok {
				return -1
			}
			return 1
		}
		if aok {
			switch {
			case am < bm:
				return -1
			case am > bm:
				return 1
			}
		}
		return 0
	})
}

// benchmarkReorder updates candidates and their trace together after a nested
// group's classifier has chosen the final effort. Only that group's ready
// candidates move; affinity, explicit rule choices, and resting fallbacks stay
// where they were put by the existing routing stages.
func benchmarkReorder(cs []candidate, pl *planned, group string, effort string, points map[benchmarkKey]benchmarkPoint) {
	if points == nil {
		return
	}
	var positions []int
	for i := range cs {
		w := pl.order[i]
		if w.Rest != nil || w.Aside || group != "" && !slices.Contains(w.Via, group) {
			continue
		}
		positions = append(positions, i)
	}
	if len(positions) < 2 {
		return
	}
	// Account/key candidates can share a model: track their original trace
	// by sorting positions rather than looking them up by model name.
	idx := slices.Clone(positions)
	minutes := func(i int) (float64, bool) {
		e := benchmarkEffort(cs[i], effort)
		if e == "" {
			return 0, false
		}
		p, ok := points[benchmarkKey{benchmarkModel(cs[i].model), e}]
		return p.AverageMinutes, ok && p.Total >= 20 && p.IQ >= 80
	}
	slices.SortStableFunc(idx, func(i, j int) int {
		a, aok := minutes(i)
		b, bok := minutes(j)
		if aok != bok {
			if aok {
				return -1
			}
			return 1
		}
		if aok && a < b {
			return -1
		}
		if aok && a > b {
			return 1
		}
		return 0
	})
	orderedCandidates := make([]candidate, len(idx))
	ordered := make([]Weighed, len(idx))
	for i, j := range idx {
		orderedCandidates[i], ordered[i] = cs[j], pl.order[j]
	}
	for i, j := range positions {
		cs[j], pl.order[j] = orderedCandidates[i], ordered[i]
	}
}
