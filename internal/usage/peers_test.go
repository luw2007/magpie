package usage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/sessions"
)

func peerHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	oldLogs := LogCalls
	LogCalls = func(time.Time) []sessions.Call { return nil }
	t.Cleanup(func() { LogCalls = oldLogs })
	ForgetPeersForTest()
	t.Cleanup(ForgetPeersForTest)
}

// A remote magpie's own days are pulled and listed beside this computer's,
// each row naming the computer it was made on; pulling again counts nothing
// twice, a call the remote only passed on is left to the computer that made
// it, and sync letting go of its days leaves the pulled ones.
func TestPullPeersAggregatesRemoteDays(t *testing.T) {
	// the remote computer: two calls of its own and one passed on from here
	peerHome(t)
	now := time.Now()
	Append(Record{Time: now.Add(-2 * time.Minute), Agent: "codex", Provider: "p", Model: "m", Input: 100, Status: 200})
	Append(Record{Time: now.Add(-time.Minute), Agent: "codex", Provider: "p", Model: "m", Input: 200, Status: 200})
	Append(Record{Time: now.Add(-time.Second), Agent: "codex", Provider: "p", Model: "m", Input: 9000, Via: "laptop", Status: 200})
	remoteID, _ := Computer()
	answer, err := json.Marshal(map[string]any{"object": "list", "data": PeerDays(now.AddDate(0, 0, -3))})
	if err != nil {
		t.Fatal(err)
	}

	// this computer, with the remote as a provider
	peerHome(t)
	var asked int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-magpie-office" || r.URL.Path != PeersPath {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		asked++
		w.Write(answer)
	}))
	t.Cleanup(srv.Close)
	if err := provider.Save(provider.Provider{ID: "office", Name: "Office", Preset: provider.RemoteMagpiePreset, Key: "sk-magpie-office", Chat: srv.URL}); err != nil {
		t.Fatal(err)
	}
	Append(Record{Time: now.Add(-3 * time.Minute), Agent: "codex", Provider: "p", Model: "m", Input: 7, Status: 200})
	selfID, _ := Computer()
	if selfID == remoteID {
		t.Fatal("two computers, one id")
	}

	if errs := PullPeers(context.Background()); len(errs) != 0 {
		t.Fatal(errs)
	}
	check := func(when string) {
		t.Helper()
		l := LedgerOf(All, Filter{})
		if len(l.Rows) != 3 || l.Sum.Input != 307 {
			t.Fatalf("%s: %d rows, %d in; want 3, 307", when, len(l.Rows), l.Sum.Input)
		}
		for _, r := range l.Rows {
			if (r.Computer == remoteID) != (r.Input != 7) {
				t.Fatalf("%s: row of %d in said of computer %q", when, r.Input, r.Computer)
			}
		}
		if names := sharedNames(); names[remoteID] == "" {
			t.Fatalf("%s: the remote computer is not named: %v", when, names)
		}
		if l := LedgerOf(All, Filter{Computer: ThisComputer}); len(l.Rows) != 1 {
			t.Fatalf("%s: this computer has %d rows", when, len(l.Rows))
		}
	}
	check("pulled")
	if errs := PullPeers(context.Background()); len(errs) != 0 {
		t.Fatal(errs)
	}
	check("pulled twice")

	// the same day also came by sync: read once
	var got struct{ Data []SharedDay }
	if err := json.Unmarshal(answer, &got); err != nil {
		t.Fatal(err)
	}
	for _, d := range got.Data {
		if err := KeepShared(d); err != nil {
			t.Fatal(err)
		}
	}
	check("also synced")
	DropAllShared() // sync turned off
	check("sync off")

	// the remote can't be asked: what it answered stays, the error is told
	srv.Close()
	errs := PullPeers(context.Background())
	if errs["office"] == nil {
		t.Fatalf("an unreachable remote isn't told: %v", errs)
	}
	check("remote down")
	if asked != 2 {
		t.Fatalf("remote asked %d times, want 2", asked)
	}
}

// How the last pull of each remote went is what the Usage page tells the
// reader: a remote just added is waiting, not empty; one that fails is sorted
// by cause, so the page can say what to fix, and keeps the days it answered
// before; one that answers again is clear of the error.
func TestPeersSayHowEachPullWent(t *testing.T) {
	peerHome(t)
	status := http.StatusUnauthorized
	var days []SharedDay
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": days})
	}))
	t.Cleanup(srv.Close)
	if err := provider.Save(provider.Provider{ID: "office", Name: "Office", Preset: provider.RemoteMagpiePreset, Key: "k", Chat: srv.URL}); err != nil {
		t.Fatal(err)
	}
	one := func() PeerStatus {
		t.Helper()
		ps := Peers()
		if len(ps) != 1 || ps[0].ID != "office" || ps[0].Name != "Office" {
			t.Fatalf("peers: %+v", ps)
		}
		return ps[0]
	}
	if s := one(); !s.Pulling || !s.At.IsZero() || s.Error != "" {
		t.Fatalf("before any pull: %+v", s)
	}
	for _, c := range []struct {
		code int
		kind string
	}{{http.StatusUnauthorized, "key"}, {http.StatusForbidden, "notShared"}, {http.StatusNotFound, "tooOld"}, {http.StatusInternalServerError, "other"}} {
		status = c.code
		PullPeers(context.Background())
		if s := one(); s.Pulling || s.Kind != c.kind || s.Error == "" || !s.At.IsZero() {
			t.Fatalf("%d: %+v, want kind %q and no success yet", c.code, s, c.kind)
		}
	}
	remote := "245eab618771c093"
	days = []SharedDay{{Computer: remote, Name: "devbox", Day: time.Now().Format(time.DateOnly), Calls: []SharedCall{{Record: Record{Time: time.Now(), Model: "m", Input: 5, Status: 200}}}}}
	status = http.StatusOK
	PullPeers(context.Background())
	ok := one()
	if ok.Error != "" || ok.Kind != "" || ok.At.IsZero() {
		t.Fatalf("after an answer: %+v", ok)
	}
	// it stops answering: the error is told, the time of the last good read stays
	status = http.StatusForbidden
	PullPeers(context.Background())
	s := one()
	if s.Kind != "notShared" || !s.At.Equal(ok.At) {
		t.Fatalf("failing after a good pull: %+v (last good %v)", s, ok.At)
	}
	if l := LedgerOf(All, Filter{Computer: remote}); len(l.Rows) != 1 {
		t.Fatalf("the days it answered are gone: %d rows", len(l.Rows))
	}
	// the page's Pull now ends in the same status
	status = http.StatusOK
	got, err := PullPeerNow(context.Background(), "office")
	if err != nil || got.Error != "" || got.At.Before(ok.At) {
		t.Fatalf("pull now: %+v, %v", got, err)
	}
	if _, err := PullPeerNow(context.Background(), "nobody"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pull now for no such remote: %v", err)
	}
	// switched off: not a peer any more
	p, err := provider.Find("office")
	if err != nil {
		t.Fatal(err)
	}
	off := *p
	off.Off = true
	if err := provider.Save(off); err != nil {
		t.Fatal(err)
	}
	if ps := Peers(); len(ps) != 0 {
		t.Fatalf("a remote switched off is still told: %+v", ps)
	}
}

// A remote's days are far larger than a quota card: devbox's one day was
// 10 MB and its whole window 42 MB. A pull that read them with the cards'
// 8 MiB limit was cut off in the middle and failed as "unexpected end of
// JSON input" every time once the remote had a busy day. A day bigger than
// that is pulled whole and kept.
func TestPullPeersTakesADayLargerThanAQuotaCard(t *testing.T) {
	peerHome(t)
	now := time.Now()
	pad := strings.Repeat("x", 4096)
	var calls []SharedCall
	for i := range 3000 { // 3000 × ~4 KiB ≈ 12 MB, past the 8 MiB the cards are read with
		calls = append(calls, SharedCall{Record: Record{Time: now.Add(-time.Duration(i) * time.Second), Model: "m", Session: pad, Input: 1, Status: 200}})
	}
	remote := "245eab618771c093"
	answer, err := json.Marshal(map[string]any{"object": "list", "data": []SharedDay{{Computer: remote, Name: "devbox", Day: now.Format(time.DateOnly), Calls: calls}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(answer) <= 8<<20 {
		t.Fatalf("the fixture is %d bytes, not past the cards' limit", len(answer))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(answer) }))
	t.Cleanup(srv.Close)
	if err := provider.Save(provider.Provider{ID: "office", Name: "Office", Preset: provider.RemoteMagpiePreset, Key: "k", Chat: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if errs := PullPeers(context.Background()); len(errs) != 0 {
		t.Fatalf("a large day was not taken: %v", errs)
	}
	if l := LedgerOf(All, Filter{Computer: remote}); len(l.Rows) != 3000 {
		t.Fatalf("%d of 3000 calls kept", len(l.Rows))
	}
}
