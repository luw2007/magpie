package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A magpie with this one as its provider reads its routing: the live trace
// since a seq, a kept day, and one request. Each is this computer's own, as
// the Routing page here shows it, told to this machine or to another that
// carries a gateway key.
func TestPeerRoutingEndpoints(t *testing.T) {
	fresh(t)
	t.Setenv("MAGPIE_ADDR", "")
	s := New()
	rt := s.trace.beginFor(httptest.NewRequest("POST", "/v1/chat/completions", nil), Route{Agent: "codex", Model: "m", Provider: "p", Order: []Weighed{}})
	s.trace.update(rt, func(r *Route) { r.Done, r.Status = true, 200 })
	h := lanGuard(s.Handler())
	get := func(from, target string, hdr ...string) (int, string) {
		t.Helper()
		r := httptest.NewRequest("GET", target, nil)
		r.RemoteAddr = from
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Set(hdr[i], hdr[i+1])
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	const here, away = "127.0.0.1:5000", "192.168.1.9:5000"

	var tr struct{ Data PeerTrace }
	if c, b := get(here, TracePath); c != http.StatusOK || json.Unmarshal([]byte(b), &tr) != nil || len(tr.Data.Routes) != 1 || tr.Data.Routes[0].ID != rt.ID || tr.Data.Now.IsZero() {
		t.Fatalf("trace: %d %.300s", c, b)
	}
	// only what changed since the seq the page has
	if c, b := get(here, TracePath+"?after="+jsonInt(tr.Data.Seq)); c != http.StatusOK || json.Unmarshal([]byte(b), &tr) != nil || len(tr.Data.Routes) != 0 {
		t.Fatalf("trace after its seq: %d %.300s", c, b)
	}
	// a wait is capped: a huge ask is answered within the cap, not held for it
	start := time.Now()
	if c, _ := get(here, TracePath+"?after="+jsonInt(tr.Data.Seq)+"&wait=1"); c != http.StatusOK || time.Since(start) > 3*time.Second {
		t.Fatalf("trace wait=1 took %v: %d", time.Since(start), c)
	}

	var hs struct{ Data PeerHistory }
	if c, b := get(here, TraceHistoryPath); c != http.StatusOK || json.Unmarshal([]byte(b), &hs) != nil || hs.Data.Days == nil || hs.Data.Routes == nil {
		t.Fatalf("history: %d %.300s", c, b)
	}

	// one request, from the live trace; none is null, not 404 (404 reads as
	// "no such API" to the magpie asking)
	var one struct{ Data *Route }
	if c, b := get(here, TraceRoutePath+"?id="+jsonInt(rt.ID)+"&day="+rt.Time.Format("2006-01-02")); c != http.StatusOK || json.Unmarshal([]byte(b), &one) != nil || one.Data == nil || one.Data.ID != rt.ID {
		t.Fatalf("route: %d %.300s", c, b)
	}
	one.Data = &Route{}
	if c, b := get(here, TraceRoutePath+"?id=1&day=2000-01-01"); c != http.StatusOK || json.Unmarshal([]byte(b), &one) != nil || one.Data != nil {
		t.Fatalf("no such route: %d %.300s", c, b)
	}
	for _, bad := range []string{"", "?id=x&day=2000-01-01", "?id=1", "?id=1&day=yesterday"} {
		if c, _ := get(here, TraceRoutePath+bad); c != http.StatusBadRequest {
			t.Errorf("route%s: %d, want 400", bad, c)
		}
	}

	// another machine needs the shared gateway's key, as the quotas do
	for _, p := range []string{TracePath, TraceHistoryPath, TraceRoutePath + "?id=1&day=2000-01-01"} {
		if c, b := get(away, p); c == http.StatusOK || strings.Contains(b, `"routes"`) {
			t.Errorf("%s without a key: %d %.200s", p, c, b)
		}
	}
	_, secrets := newCaller(t, "Peer")
	if c, b := get(away, TracePath, "Authorization", "Bearer "+secrets[0]); c != http.StatusOK || !strings.Contains(b, `"routes"`) {
		t.Fatalf("trace with a key: %d %.200s", c, b)
	}
}
