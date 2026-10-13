package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
