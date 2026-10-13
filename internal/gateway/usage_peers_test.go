package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/usage"
)

// GET /v1/magpie/usage/days tells another magpie this computer's own days of
// calls: to this machine, never to another without a gateway key, and a call
// this computer only passed on is left to the one that made it.
func TestUsageDaysEndpoint(t *testing.T) {
	fresh(t)
	t.Setenv("MAGPIE_ADDR", "")
	now := time.Now()
	usage.Append(usage.Record{Time: now.Add(-time.Minute), Agent: "codex", Provider: "p", Model: "m", Input: 11, Status: 200})
	usage.Append(usage.Record{Time: now.Add(-time.Second), Agent: "codex", Provider: "p", Model: "m", Input: 5000, Via: "laptop", Status: 200})
	h := lanGuard(New().Handler())
	get := func(from string) (int, string) {
		r := httptest.NewRequest("GET", usage.PeersPath, nil)
		r.RemoteAddr = from
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	c, body := get("127.0.0.1:5000")
	var out struct{ Data []usage.SharedDay }
	if c != http.StatusOK || json.Unmarshal([]byte(body), &out) != nil {
		t.Fatalf("this machine: %d %.200s", c, body)
	}
	var in, calls int
	for _, d := range out.Data {
		if d.Computer == "" || d.Name == "" {
			t.Fatalf("a day with no computer: %+v", d)
		}
		for _, call := range d.Calls {
			in += call.Input
			calls++
		}
	}
	if calls != 1 || in != 11 {
		t.Fatalf("%d calls, %d in; want the one call made here", calls, in)
	}
	if c, b := get("192.168.1.9:5000"); c == http.StatusOK || strings.Contains(b, `"calls"`) {
		t.Fatalf("another machine without a key got the days: %d %.200s", c, b)
	}
}
