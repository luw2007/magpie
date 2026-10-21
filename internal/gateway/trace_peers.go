package gateway

import (
	"net/http"
	"strconv"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// What another magpie that has this one as its provider (remote-magpie) may
// read of its routing, to show it on its own Routing page: the live trace,
// a kept day, and one request of either. Read-only; nothing here is kept
// there. This peer API has its own routing namespace and data envelope:
// /v1/magpie/trace is the loopback-only owner API, never a shared peer API.
const (
	PeerTracePath        = "/v1/magpie/routing/trace"
	PeerTraceHistoryPath = "/v1/magpie/routing/trace/history"
	PeerTraceRoutePath   = "/v1/magpie/routing/trace/route"
)

// peerTraceWaitMax caps how long one trace ask waits for a change: the
// Routing page's own long poll waits as long.
const peerTraceWaitMax = 25 * time.Second

// PeerTrace is PeerTracePath's answer: the trace since a seq, with this
// computer's clock for the page to measure its flights by.
type PeerTrace struct {
	TraceState
	Now time.Time `json:"now"`
}

// PeerHistory is PeerTraceHistoryPath's answer: the days kept, and the routes of
// the day asked for.
type PeerHistory struct {
	Days   []HistoryDay `json:"days"`
	Routes []Route      `json:"routes"`
	Cut    bool         `json:"cut"`
}

// peerRoutesAllowed lets a request in as the quotas do: this machine, or
// another with the shared gateway's key; a key held to a budget, models or
// accounts is told none of it, as the routes of every key's requests are not
// its to see.
func peerRoutesAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !local(r) && !sharedWith(r) {
		writeError(w, provider.Chat, http.StatusForbidden, "magpie's routing is told to another machine only when magpie is shared on the local network (Settings → Share on local network) and the request carries its API key (Authorization: Bearer <key> or x-api-key: <key>)")
		return false
	}
	return !refuseHeld(w, r, heldRefused)
}

// peerTrace is the routes changed after ?after=, waiting up to ?wait=
// seconds (25 at most) for one.
func (s *Server) peerTrace(w http.ResponseWriter, r *http.Request) {
	if !peerRoutesAllowed(w, r) {
		return
	}
	q := r.URL.Query()
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	secs, _ := strconv.ParseFloat(q.Get("wait"), 64)
	wait := min(max(time.Duration(secs*float64(time.Second)), 0), peerTraceWaitMax)
	st := s.Trace(r.Context(), after, wait)
	writeJSON(w, 200, map[string]any{"object": "trace", "data": PeerTrace{TraceState: st, Now: time.Now()}})
}

// peerHistory is the days kept and the routes of ?day=, as the history is on
// disk.
func (s *Server) peerHistory(w http.ResponseWriter, r *http.Request) {
	if !peerRoutesAllowed(w, r) {
		return
	}
	days, routes, cut := History(r.URL.Query().Get("day"))
	if days == nil {
		days = []HistoryDay{}
	}
	writeJSON(w, 200, map[string]any{"object": "trace", "data": PeerHistory{Days: days, Routes: routes, Cut: cut}})
}

// peerRoute is one request, ?id= on ?day=: from the live trace, else from
// the day's history. Told null, not 404, when there is none: a 404 reads as
// "no such API" to the magpie asking.
func (s *Server) peerRoute(w http.ResponseWriter, r *http.Request) {
	if !peerRoutesAllowed(w, r) {
		return
	}
	q := r.URL.Query()
	id, err := strconv.ParseInt(q.Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, provider.Chat, http.StatusBadRequest, "invalid route id")
		return
	}
	var found *Route
	for _, route := range s.Trace(r.Context(), 0, 0).Routes {
		if route.ID == id {
			found = &route
			break
		}
	}
	if found == nil {
		day, err := time.Parse("2006-01-02", q.Get("day"))
		if err != nil {
			writeError(w, provider.Chat, http.StatusBadRequest, "invalid route day")
			return
		}
		if route, ok := HistoryRoute(id, day); ok {
			found = &route
		}
	}
	writeJSON(w, 200, map[string]any{"object": "trace", "data": found})
}
