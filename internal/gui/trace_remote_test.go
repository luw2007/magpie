package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

// ?source=<remote magpie> turns the Routing page's three reads to that
// computer: its trace, its kept days and one of its requests, told as the
// local ones are. A source that is no remote magpie here is a 404, never
// the local routing under another name; a remote that can't answer is a 502
// that says what kind of failure it is; and with no source the local
// routing is read as before.
func TestRoutingReadsARemoteMagpie(t *testing.T) {
	sandboxHome(t)
	var asked []string
	status := http.StatusOK
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.RequestURI()+" "+r.Header.Get("Authorization"))
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		route := gateway.Route{ID: 42, Seq: 7, Time: time.Now(), Agent: "codex", Model: "gpt", Provider: "p", Order: []gateway.Weighed{}, Tries: []gateway.Try{}, Done: true, Status: 200,
			Usage: []gateway.RouteUsage{{Provider: "p", Model: "gpt", Input: 1000, Output: 100}}}
		switch r.URL.Path {
		case gateway.PeerTracePath:
			json.NewEncoder(w).Encode(map[string]any{"data": gateway.PeerTrace{TraceState: gateway.TraceState{Seq: 7, Routes: []gateway.Route{route}}, Now: time.Now()}})
		case gateway.PeerTraceHistoryPath:
			json.NewEncoder(w).Encode(map[string]any{"data": gateway.PeerHistory{Days: []gateway.HistoryDay{{Day: "2026-10-14", Requests: 1}}, Routes: []gateway.Route{route}}})
		case gateway.PeerTraceRoutePath:
			if r.URL.Query().Get("id") == "42" {
				json.NewEncoder(w).Encode(map[string]any{"data": route})
			} else {
				w.Write([]byte(`{"data":null}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(remote.Close)
	if err := provider.Save(provider.Provider{ID: "office", Name: "Office", Preset: provider.RemoteMagpiePreset, Key: "secret", Chat: remote.URL}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	traceRoutes(mux)
	call := func(path string) (int, string) {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w.Code, w.Body.String()
	}

	var tr traceJSON
	code, body := call("/api/gateway/trace?source=office&after=3&wait=1")
	if code != 200 || json.Unmarshal([]byte(body), &tr) != nil || !tr.Mine || tr.Seq != 7 || len(tr.Routes) != 1 || tr.Routes[0].ID != 42 {
		t.Fatalf("trace: %d %.300s", code, body)
	}
	if !tr.Routes[0].Priced && tr.Routes[0].Unpriced == 0 {
		t.Fatalf("the remote's route is not priced here: %+v", tr.Routes[0])
	}
	if want := "/v1/magpie/routing/trace?after=3&wait=25 Bearer secret"; asked[len(asked)-1] != want {
		t.Fatalf("asked %q, want %q", asked[len(asked)-1], want)
	}

	var h struct {
		Days   []gateway.HistoryDay `json:"days"`
		Routes []routeJSON          `json:"routes"`
	}
	if code, body = call("/api/gateway/history?source=office&day=2026-10-14"); code != 200 || json.Unmarshal([]byte(body), &h) != nil || len(h.Days) != 1 || len(h.Routes) != 1 {
		t.Fatalf("history: %d %.300s", code, body)
	}
	if code, body = call("/api/gateway/route?source=office&id=42&day=2026-10-14"); code != 200 || !strings.Contains(body, `"id":42`) {
		t.Fatalf("route: %d %.300s", code, body)
	}
	if code, _ = call("/api/gateway/route?source=office&id=7&day=2026-10-14"); code != http.StatusNotFound {
		t.Fatalf("a route the remote has not: %d, want 404", code)
	}
	if code, _ = call("/api/gateway/route?source=office&id=x&day=2026-10-14"); code != http.StatusBadRequest {
		t.Fatalf("a bad id: %d, want 400", code)
	}

	// no remote magpie of that name: not the local routing in its place
	for _, p := range []string{"/api/gateway/trace", "/api/gateway/history", "/api/gateway/route?id=1&day=2026-10-14"} {
		n := len(asked)
		if code, _ = call(p + map[bool]string{true: "&", false: "?"}[strings.Contains(p, "?")] + "source=nobody"); code != http.StatusNotFound {
			t.Errorf("%s with no such source: %d, want 404", p, code)
		}
		if len(asked) != n {
			t.Errorf("%s with no such source asked a remote", p)
		}
	}

	// failures say their kind, for the page to word its advice by
	for _, c := range []struct {
		status int
		kind   string
	}{{http.StatusUnauthorized, "key"}, {http.StatusForbidden, "notShared"}, {http.StatusNotFound, "tooOld"}} {
		status = c.status
		var e struct{ Error, Kind string }
		if code, body = call("/api/gateway/trace?source=office"); code != http.StatusBadGateway || json.Unmarshal([]byte(body), &e) != nil || e.Kind != c.kind || e.Error == "" {
			t.Errorf("remote answering %d: %d %s, want 502 of kind %s", c.status, code, body, c.kind)
		}
	}
	status = http.StatusOK
	remote.Close()
	var e struct{ Kind string }
	if code, body = call("/api/gateway/history?source=office"); code != http.StatusBadGateway || json.Unmarshal([]byte(body), &e) != nil || e.Kind != "unreachable" {
		t.Errorf("remote down: %d %s", code, body)
	}

	// without a source it is still this computer's routing: no gateway
	// served here, so none of it is mine, and no remote was asked
	n := len(asked)
	if code, body = call("/api/gateway/trace"); code != 200 || json.Unmarshal([]byte(body), &tr) != nil || tr.Mine || len(asked) != n {
		t.Fatalf("local trace: %d %s", code, body)
	}
}
