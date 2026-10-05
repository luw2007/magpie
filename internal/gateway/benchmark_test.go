package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

func benchmarkCandidates(models ...string) []candidate {
	cs := make([]candidate, len(models))
	for i, model := range models {
		cs[i] = candidate{p: provider.Provider{ID: "vendor"}, model: model, rest: "vendor"}
	}
	return cs
}

func benchmarkModels(cs []candidate) string {
	models := make([]string, len(cs))
	for i, c := range cs {
		models[i] = c.model
	}
	return strings.Join(models, ",")
}

func TestBenchmarkOrderExactEffortThresholdsAndStableFallback(t *testing.T) {
	points := map[benchmarkKey]benchmarkPoint{
		{"fast", "high"}:     {Total: 20, IQ: 80, AverageMinutes: 2},
		{"steady", "high"}:   {Total: 24, IQ: 85, AverageMinutes: 4},
		{"tie", "high"}:      {Total: 30, IQ: 90, AverageMinutes: 4},
		{"slow", "high"}:     {Total: 19, IQ: 120, AverageMinutes: 1},
		{"weak", "high"}:     {Total: 30, IQ: 79.99, AverageMinutes: 0.5},
		{"medium", "medium"}: {Total: 30, IQ: 99, AverageMinutes: 0.1},
	}
	cs := benchmarkCandidates("unknown", "SLOW", "steady", "tie", "weak", "medium", "fast")
	benchmarkOrder(cs, "high", points)
	if got, want := benchmarkModels(cs), "fast,steady,tie,unknown,SLOW,weak,medium"; got != want {
		t.Fatalf("high order %s, want %s", got, want)
	}
	cs = benchmarkCandidates("unknown", "steady", "fast")
	benchmarkOrder(cs, "", points)
	if got := benchmarkModels(cs); got != "unknown,steady,fast" {
		t.Fatalf("no effort must keep original order: %s", got)
	}
	cs = benchmarkCandidates("fast", "medium", "steady")
	benchmarkOrder(cs, "medium", points)
	if got := benchmarkModels(cs); got != "medium,fast,steady" {
		t.Fatalf("different thinking levels must not match: %s", got)
	}
}

func TestBenchmarkOrderUsesEachCandidateEffort(t *testing.T) {
	points := map[benchmarkKey]benchmarkPoint{
		{"gpt-5.6-luna", "xhigh"}:         {Total: 30, IQ: 90, AverageMinutes: 4},
		{"deepseek-v4-flash", "max"}:      {Total: 30, IQ: 90, AverageMinutes: 1},
		{"gemini-3.8-flash-high", "high"}: {Total: 30, IQ: 90, AverageMinutes: 2},
		{"glm-5.3-flash", "high"}:         {Total: 30, IQ: 90, AverageMinutes: 3},
	}
	cs := []candidate{
		{model: "gpt-5.6-luna", effort: "xhigh"},
		{model: "deepseek-v4-flash", effort: "max"},
		{model: "google/gemini-3.8-flash-high", effort: "high"},
		{model: "glm-5.3-flash", effort: "high"},
	}
	benchmarkOrder(cs, "low", points)
	if got := benchmarkModels(cs); got != "deepseek-v4-flash,google/gemini-3.8-flash-high,glm-5.3-flash,gpt-5.6-luna" {
		t.Fatalf("per-candidate effort order: %s", got)
	}
}

func TestBenchmarkModelIdentity(t *testing.T) {
	points := map[benchmarkKey]benchmarkPoint{
		{"gemini-3.8-flash-high", "high"}: {Total: 90, IQ: 81, AverageMinutes: 2},
		{"glm-5.3-flash", "high"}:         {Total: 90, IQ: 81, AverageMinutes: 3},
		{"deepseek-v4-flash", "high"}:     {Total: 90, IQ: 81, AverageMinutes: 4},
	}
	cs := benchmarkCandidates("glm-5.3-flash", "google/gemini-3.8-flash-high", "DeepSeek-V4-Flash", "gemini-3.8-flash-lite-high")
	benchmarkOrder(cs, "high", points)
	if got := benchmarkModels(cs); got != "google/gemini-3.8-flash-high,glm-5.3-flash,DeepSeek-V4-Flash,gemini-3.8-flash-lite-high" {
		t.Fatalf("model identity: %s", got)
	}
}

func TestBenchmarkSnapshotCacheAndFailOpen(t *testing.T) {
	requests, fail := 0, false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if fail {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte(`{"points":[{"model":"fast","effort":"high","total":20,"iq":80,"average_minutes":1}]}`))
	}))
	defer up.Close()
	cache := &benchmarkSnapshot{client: up.Client(), url: up.URL}
	for range 2 {
		cs := benchmarkCandidates("unknown", "fast")
		benchmarkOrder(cs, "high", cache.get())
		if got := benchmarkModels(cs); got != "fast,unknown" {
			t.Fatalf("successful snapshot: %s", got)
		}
	}
	if requests != 1 {
		t.Fatalf("TTL must avoid repeated requests: %d", requests)
	}
	fail = true
	cache.expires = cache.expires.Add(-benchmarkTTL)
	cs := benchmarkCandidates("unknown", "fast")
	benchmarkOrder(cs, "high", cache.get())
	if got := benchmarkModels(cs); got != "unknown,fast" {
		t.Fatalf("failure must keep configured order: %s", got)
	}
	cache.get()
	if requests != 2 {
		t.Fatalf("negative TTL must avoid repeated failures: %d", requests)
	}
	cache.expires = cache.expires.Add(-benchmarkFailureTTL)
	fail = false
	if got := cache.get(); len(got) != 1 || requests != 3 {
		t.Fatalf("refresh failed: points %v, requests %d", got, requests)
	}
}

func TestBenchmarkInvalidSnapshotFailsOpen(t *testing.T) {
	for _, body := range []string{`{}`, `{"points":[]}`, `{"points":[{"model":"fast","effort":"high","total":30,"iq":99}]}`, `{"points":[{"model":"fast","effort":"high","total":30,"iq":99,"average_minutes":0}]}`, `{"points":[{"model":"fast","effort":"high","total":30,"iq":99,"average_minutes":1}, {"model":"fast","effort":"high","total":30,"iq":99,"average_minutes":2}]}`, `broken`} {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		cache := &benchmarkSnapshot{client: up.Client(), url: up.URL}
		if got := cache.get(); got != nil {
			t.Errorf("invalid payload %q returned %v", body, got)
		}
		up.Close()
	}
}

func TestBenchmarkSnapshotIgnoresZeroSampleRows(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"points":[{"model":"pending","effort":"high","total":0,"iq":null,"average_minutes":null},{"model":"ready","effort":"high","total":20,"iq":80,"average_minutes":2}]}`))
	}))
	defer up.Close()
	cache := &benchmarkSnapshot{client: up.Client(), url: up.URL}
	if got := cache.get(); len(got) != 1 {
		t.Fatalf("zero-sample row should be unknown: %v", got)
	}
}

func TestBenchmarkGroupRoutesByRequestEffortAndTracesStrategy(t *testing.T) {
	fresh(t)
	slow, fast := &keyed{}, &keyed{}
	serveOn(t, "slow", "ks", []string{"slow-model"}, slow)
	serveOn(t, "fast", "kf", []string{"fast-model"}, fast)
	if err := provider.SaveGroup(provider.Group{Name: "Quick", Members: []string{"slow/slow-model", "fast/fast-model"}, Routing: provider.Benchmark}); err != nil {
		t.Fatal(err)
	}
	old := deepSWE
	deepSWE = &benchmarkSnapshot{points: map[benchmarkKey]benchmarkPoint{
		{"slow-model", "high"}: {Total: 30, IQ: 90, AverageMinutes: 8},
		{"fast-model", "high"}: {Total: 30, IQ: 90, AverageMinutes: 2},
	}, expires: time.Now().Add(time.Hour)}
	t.Cleanup(func() { deepSWE = old })
	s := New()
	code, body := postAs(t, s, "effort", `{"model":"group/quick","reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK || !strings.Contains(body, "from kf") {
		t.Fatalf("benchmark route: %d %s", code, body)
	}
	route := s.trace.routes[len(s.trace.routes)-1]
	if route.Group.Routing != provider.Benchmark || route.Order[0].Provider != "fast" || route.Order[0].Routing != provider.Benchmark {
		t.Fatalf("benchmark trace: group %+v, order %+v", route.Group, route.Order)
	}
	code, body = postAs(t, s, "plain", `{"model":"group/quick","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK || !strings.Contains(body, "from ks") {
		t.Fatalf("no-effort configured order: %d %s", code, body)
	}
}

func TestBenchmarkUsesJevFinalEffort(t *testing.T) {
	s, _, _, j := jevved(t, provider.EffortAuto)
	j.score = 2.4 // high, rather than the client's low
	g, _, _ := provider.FindGroup("group/r")
	g.Routing = provider.Benchmark
	if err := provider.SaveGroup(g); err != nil {
		t.Fatal(err)
	}
	old := deepSWE
	deepSWE = &benchmarkSnapshot{points: map[benchmarkKey]benchmarkPoint{
		{"small", "low"}:  {Total: 30, IQ: 90, AverageMinutes: 1},
		{"big", "high"}:   {Total: 30, IQ: 90, AverageMinutes: 1},
		{"small", "high"}: {Total: 30, IQ: 90, AverageMinutes: 9},
	}, expires: time.Now().Add(time.Hour)}
	t.Cleanup(func() { deepSWE = old })
	code, body := postAs(t, s, "jev", chat("find the race", nil, 0, `,"reasoning_effort":"low"`))
	if code != http.StatusOK || !strings.Contains(body, "from kb") {
		t.Fatalf("Jev high must rank high benchmark points: %d %s", code, body)
	}
	route := s.trace.routes[len(s.trace.routes)-1]
	if route.Rule == nil || route.Rule.Pick != "high" || route.Order[0].Model != "big" || route.Effort != "low" {
		t.Fatalf("Jev route: %+v", route)
	}
}

func TestBenchmarkNestedWithinOrderedGroup(t *testing.T) {
	fresh(t)
	a, b, c := &keyed{}, &keyed{}, &keyed{}
	serveOn(t, "a", "ka", []string{"unmatched"}, a)
	serveOn(t, "b", "kb", []string{"slow-model"}, b)
	serveOn(t, "c", "kc", []string{"fast-model"}, c)
	if err := provider.SaveGroup(provider.Group{Name: "Inner", Members: []string{"b/slow-model", "c/fast-model"}, Routing: provider.Benchmark}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{Name: "Outer", Members: []string{"a/unmatched", "group/inner"}, Routing: provider.Ordered}); err != nil {
		t.Fatal(err)
	}
	old := deepSWE
	deepSWE = &benchmarkSnapshot{points: map[benchmarkKey]benchmarkPoint{
		{"slow-model", "high"}: {Total: 30, IQ: 90, AverageMinutes: 9},
		{"fast-model", "high"}: {Total: 30, IQ: 90, AverageMinutes: 2},
	}, expires: time.Now().Add(time.Hour)}
	t.Cleanup(func() { deepSWE = old })
	s := New()
	code, body := postAs(t, s, "nested", `{"model":"group/outer","reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK || !strings.Contains(body, "from ka") {
		t.Fatalf("outer order: %d %s", code, body)
	}
	route := s.trace.routes[len(s.trace.routes)-1]
	if got := route.Order; len(got) != 3 || got[0].Provider != "a" || got[1].Provider != "c" || got[2].Provider != "b" || route.Group.Subs[0].Routing != provider.Benchmark {
		t.Fatalf("nested benchmark order: %+v", route)
	}
}

func TestBenchmarkNestedJevFinalEffort(t *testing.T) {
	s, _, _, j := jevved(t, provider.EffortAuto)
	j.score = 2.4 // high
	inner, _, _ := provider.FindGroup("group/r")
	inner.Name = "Inner"
	inner.ID = "inner"
	inner.Routing = provider.Benchmark
	if err := provider.SaveGroup(inner); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{Name: "Outer", Members: []string{"group/inner"}, Routing: provider.Ordered}); err != nil {
		t.Fatal(err)
	}
	old := deepSWE
	deepSWE = &benchmarkSnapshot{points: map[benchmarkKey]benchmarkPoint{
		{"small", "low"}:  {Total: 30, IQ: 90, AverageMinutes: 1},
		{"big", "high"}:   {Total: 30, IQ: 90, AverageMinutes: 1},
		{"small", "high"}: {Total: 30, IQ: 90, AverageMinutes: 9},
	}, expires: time.Now().Add(time.Hour)}
	t.Cleanup(func() { deepSWE = old })
	code, body := postAs(t, s, "nested-jev", `{"model":"group/outer","reasoning_effort":"low","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK || !strings.Contains(body, "from kb") {
		t.Fatalf("nested Jev high must rank high points: %d %s", code, body)
	}
	route := s.trace.routes[len(s.trace.routes)-1]
	if len(route.Nested) != 1 || route.Nested[0].Rule.Pick != "high" || route.Order[0].Model != "big" {
		t.Fatalf("nested Jev route: %+v", route)
	}
}
