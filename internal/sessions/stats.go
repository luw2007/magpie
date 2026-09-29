package sessions

import (
	"sort"
	"time"
)

// Stats is what every session spent, day by day: all of them, not only the
// latest List reads.
type Stats struct {
	From string `json:"from"` // the first date, local: the range's, or for all time the first with anything
	To   string `json:"to"`   // today
	Days []Day  `json:"days"` // the dates with anything, in order
}

// Day is one local date's usage, cut fine enough for the page to filter
// it by agent, model and folder itself.
type Day struct {
	Date   string   `json:"date"`
	Usage  []Usage  `json:"usage"`  // by agent, folder and model
	Active []Active `json:"active"` // by agent and folder
}

// Usage is what one model spent for one agent in one folder on a day.
type Usage struct {
	Agent string `json:"agent"`
	Cwd   string `json:"cwd"`
	Model string `json:"model"`
	Tokens
	Cost   float64 `json:"cost"` // USD at list price, when priced
	Priced bool    `json:"priced"`
}

// Active is the time an agent's sessions in one folder were at work on a
// day, in seconds (see day.Active for how it is told).
type Active struct {
	Agent   string `json:"agent"`
	Cwd     string `json:"cwd"`
	Seconds int64  `json:"seconds"`
}

// StatsFor reads the usage of the last days days up to today, today
// included (every day when 0), from every session file there is. Only the
// files written to since the range began are looked at; what is read is
// kept, as List's is.
func StatsFor(days int) Stats {
	return statsAt(days, time.Now())
}

// StatsAt is StatsFor as if it were now.
func StatsAt(days int, now time.Time) Stats { return statsAt(days, now) }

func statsAt(days int, now time.Time) Stats {
	now = now.In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	var since time.Time
	from := ""
	if days > 0 {
		since = today.AddDate(0, 0, 1-days)
		from = since.Format(time.DateOnly)
	}
	out := Stats{From: from, To: today.Format(time.DateOnly), Days: []Day{}}

	mu.Lock()
	defer mu.Unlock()
	loadCache()
	defer closeDBs()
	files := allFiles()
	var want []file
	for _, f := range files {
		if !f.mod.Before(since) {
			want = append(want, f)
		}
	}
	refresh(want, files)

	// a subagent's file counts in the folder of its session's own file
	folder := map[string]string{}
	for _, f := range files {
		if st := cache[f.path]; f.main && st != nil && st.Cwd != "" && folder[f.key] == "" {
			folder[f.key] = st.Cwd
		}
	}
	type uk struct{ date, agent, cwd, model string }
	type ak struct{ date, agent, cwd string }
	use := map[uk]Tokens{}
	act := map[ak]int64{}
	dates := map[string]bool{}
	for _, f := range want {
		st := cache[f.path]
		if st == nil {
			continue
		}
		cwd := folder[f.key]
		if cwd == "" {
			cwd = st.Cwd
		}
		for date, d := range st.Days {
			if date == "" || date < from || date > out.To {
				continue
			}
			for model, t := range d.Models {
				if t.zero() {
					continue
				}
				k := uk{date, f.agent, cwd, model}
				u := use[k]
				u.add(t)
				use[k] = u
				dates[date] = true
			}
			if d.Active > 0 {
				act[ak{date, f.agent, cwd}] += d.Active
				dates[date] = true
			}
		}
	}

	byDate := map[string]*Day{}
	for date := range dates {
		byDate[date] = &Day{Date: date, Usage: []Usage{}, Active: []Active{}}
	}
	price := pricer()
	for k, t := range use {
		u := Usage{Agent: k.agent, Cwd: k.cwd, Model: k.model, Tokens: t}
		if p := price(k.model); p != nil {
			u.Cost, u.Priced = p.Cost(t.Input, t.Output, t.CacheRead, t.CacheWrite), true
		}
		byDate[k.date].Usage = append(byDate[k.date].Usage, u)
	}
	for k, ms := range act {
		byDate[k.date].Active = append(byDate[k.date].Active, Active{Agent: k.agent, Cwd: k.cwd, Seconds: ms / 1000})
	}
	for _, d := range byDate {
		sort.Slice(d.Usage, func(i, j int) bool {
			a, b := d.Usage[i], d.Usage[j]
			if a.Agent != b.Agent {
				return a.Agent < b.Agent
			}
			if a.Cwd != b.Cwd {
				return a.Cwd < b.Cwd
			}
			return a.Model < b.Model
		})
		sort.Slice(d.Active, func(i, j int) bool {
			a, b := d.Active[i], d.Active[j]
			if a.Agent != b.Agent {
				return a.Agent < b.Agent
			}
			return a.Cwd < b.Cwd
		})
		out.Days = append(out.Days, *d)
	}
	sort.Slice(out.Days, func(i, j int) bool { return out.Days[i].Date < out.Days[j].Date })
	if out.From == "" {
		out.From = out.To
		if len(out.Days) > 0 && out.Days[0].Date < out.To {
			out.From = out.Days[0].Date
		}
	}
	return out
}
