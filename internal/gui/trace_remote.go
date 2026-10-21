package gui

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

// The Routing page can look at another computer's routing instead of this
// one's: ?source=<id of a remote-magpie provider> on its three reads. What
// is read is that computer's own decisions, weighed by its own providers and
// quota, so it is shown in place of this computer's, never mixed in, and
// nothing of it is kept here.

// remoteSource is the remote magpie ?source= names; ok is false when none
// is named. An id that is no remote magpie here is answered 404.
func remoteSource(rw http.ResponseWriter, r *http.Request) (p provider.Provider, ok bool) {
	id := r.URL.Query().Get("source")
	if id == "" {
		return p, false
	}
	for _, rm := range provider.RemoteMagpies() {
		if rm.ID == id {
			return rm, true
		}
	}
	http.Error(rw, "no such remote magpie", http.StatusNotFound)
	return p, true
}

// remoteFailed answers a read the remote did not answer: 502, with what went
// wrong and its kind (provider.RemoteErrorKind) for the page to say what to do.
func remoteFailed(rw http.ResponseWriter, err error) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusBadGateway)
	writeJSON(rw, map[string]string{"error": err.Error(), "kind": provider.RemoteErrorKind(err)})
}

// remoteTrace is the remote's trace since ?after=, in the shape of the local
// one: Mine is true, as it is the serving gateway's own.
func remoteTrace(rw http.ResponseWriter, r *http.Request, p provider.Provider) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	path := gateway.PeerTracePath + "?after=" + strconv.FormatInt(after, 10)
	if r.URL.Query().Get("wait") != "" {
		path += "&wait=25"
	}
	var got gateway.PeerTrace
	if err := provider.RemoteGet(r.Context(), p, path, &got); err != nil {
		remoteFailed(rw, err)
		return
	}
	if got.Routes == nil {
		got.Routes = []gateway.Route{}
	}
	writeJSON(rw, traceJSON{TraceState: got.TraceState, Mine: true, Now: got.Now, Routes: priceRoutes(got.Routes, nil)})
}

// remoteHistory is the remote's kept days and the routes of ?day=.
func remoteHistory(rw http.ResponseWriter, r *http.Request, p provider.Provider) {
	var got gateway.PeerHistory
	if err := provider.RemoteGet(r.Context(), p, gateway.PeerTraceHistoryPath+"?day="+url.QueryEscape(r.URL.Query().Get("day")), &got); err != nil {
		remoteFailed(rw, err)
		return
	}
	if got.Days == nil {
		got.Days = []gateway.HistoryDay{}
	}
	writeJSON(rw, map[string]any{"days": got.Days, "routes": priceRoutes(got.Routes, nil), "cut": got.Cut})
}

// remoteRoute is one of the remote's requests, 404 when it has none.
func remoteRoute(rw http.ResponseWriter, r *http.Request, p provider.Provider) {
	q := r.URL.Query()
	if _, err := strconv.ParseInt(q.Get("id"), 10, 64); err != nil {
		http.Error(rw, "invalid route id", http.StatusBadRequest)
		return
	}
	if _, err := time.Parse("2006-01-02", q.Get("day")); err != nil {
		http.Error(rw, "invalid route day", http.StatusBadRequest)
		return
	}
	var got *gateway.Route
	path := gateway.PeerTraceRoutePath + "?id=" + url.QueryEscape(q.Get("id")) + "&day=" + url.QueryEscape(q.Get("day"))
	if err := provider.RemoteGet(r.Context(), p, path, &got); err != nil {
		remoteFailed(rw, err)
		return
	}
	if got == nil {
		http.Error(rw, "route history is no longer available", http.StatusNotFound)
		return
	}
	writeJSON(rw, priceRoutes([]gateway.Route{*got}, nil)[0])
}
