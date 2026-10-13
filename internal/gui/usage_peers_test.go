package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/usage"
)

// The ledger tells the page how each remote magpie it pulls usage from last
// went, and the page's Pull now asks for one of them at once: a remote that
// refuses says why, in the kind the page words its advice by, and one that is
// not in use is a 404, not a pull of something else.
func TestUsagePeersInLedgerAndPullNow(t *testing.T) {
	sandboxHome(t)
	usage.ForgetPeersForTest()
	t.Cleanup(usage.ForgetPeersForTest)
	status := http.StatusForbidden
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	mux := http.NewServeMux()
	usageRoutes(mux, folderOnly{})
	call := func(method, path, body string) (int, string) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w.Code, w.Body.String()
	}
	ledger := func() ledgerJSON {
		t.Helper()
		code, body := call("GET", "/api/usage/requests?period=all", "")
		var l ledgerJSON
		if code != 200 || json.Unmarshal([]byte(body), &l) != nil {
			t.Fatalf("ledger: %d %s", code, body)
		}
		return l
	}
	if l := ledger(); len(l.Peers) != 0 {
		t.Fatalf("peers with no remote magpie: %+v", l.Peers)
	}
	if err := provider.Save(provider.Provider{ID: "office", Name: "Office", Preset: provider.RemoteMagpiePreset, Key: "k", Chat: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if l := ledger(); len(l.Peers) != 1 || !l.Peers[0].Pulling || l.Peers[0].Name != "Office" {
		t.Fatalf("a remote not yet pulled is waiting: %+v", l.Peers)
	}
	code, body := call("POST", "/api/usage/peers/pull", `{"id":"office"}`)
	var s usage.PeerStatus
	if code != 200 || json.Unmarshal([]byte(body), &s) != nil || s.Kind != "notShared" || s.Error == "" || s.Pulling {
		t.Fatalf("pull now into a remote that isn't shared: %d %s", code, body)
	}
	if l := ledger(); len(l.Peers) != 1 || l.Peers[0].Kind != "notShared" {
		t.Fatalf("the ledger follows the pull: %+v", l.Peers)
	}
	for _, c := range []struct {
		body string
		want int
	}{
		{`{"id":"nobody"}`, http.StatusNotFound},
		{`{}`, http.StatusBadRequest},
		{`not json`, http.StatusBadRequest},
	} {
		if code, _ := call("POST", "/api/usage/peers/pull", c.body); code != c.want {
			t.Fatalf("pull %s: %d, want %d", c.body, code, c.want)
		}
	}
}
