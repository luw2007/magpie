package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A decision provider is only ever a group's classifier: its models aren't
// in the catalog nor a group's members, and a group's effort is picked only
// by it.
func TestDecider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Save(Provider{ID: "a", Name: "a", Key: "k", Chat: "http://127.0.0.1:1/v1", Models: []string{"m"}}); err != nil {
		t.Fatal(err)
	}
	ts, err := FromPreset("typesafe")
	if err != nil || !ts.Decides() {
		t.Fatalf("preset %+v %v", ts, err)
	}
	ts.Key = "kts"
	if err := Save(ts); err != nil {
		t.Fatal(err)
	}
	if p, err := Find("typesafe"); err != nil || p.Host() != "api.typesafe.ai" {
		t.Fatalf("saved %+v %v", p, err)
	}
	for _, e := range Catalog() {
		if e.Provider.ID == "typesafe" {
			t.Fatalf("Jev in the catalog: %+v", e)
		}
	}
	if ds := Deciders(); len(ds) == 0 || ds[0].ID != "typesafe/jev-latest" || !IsDecider("typesafe/jev-latest") || IsDecider("a/m") {
		t.Fatalf("deciders %+v", ds)
	}
	if p, _, ok := Resolve("jev-latest"); ok && p.Decides() {
		t.Fatal("a bare jev-latest resolves to the decider")
	}
	g := Group{Name: "G", Members: []string{"a/m"}}
	for _, tc := range []struct {
		effort, classifier, err string
	}{
		{"auto", "", "needs the group's classifier"},
		{"auto", "b/m", "knows no model"},
		{"auto", "a/m", ""}, // any model, asked in words
		{"high", "typesafe/jev-latest", "not \"high\""},
		{"auto", "typesafe/jev-latest", ""},
	} {
		g.Effort, g.Classifier = tc.effort, tc.classifier
		err := SaveGroup(g)
		if tc.err == "" && err != nil || tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("%s by %q: %v, want %q", tc.effort, tc.classifier, err, tc.err)
		}
	}
	if g, _, _ := FindGroup("group/g"); g.Effort != EffortAuto || g.Classifier != "typesafe/jev-latest" || !g.Ruled() {
		t.Fatalf("saved %+v", g)
	}
	if err := SaveGroup(Group{Name: "H", Members: []string{"typesafe/jev-latest"}}); err == nil {
		t.Error("Jev saved as a group's member")
	}
}

// TypeSafe's errors are FastAPI's: {"detail":{"message":…}}.
func TestAPIErrorDetail(t *testing.T) {
	b := []byte(`{"detail":{"error_type":"authentication_error","message":"Must supply an API key! Check your request and try again."}}`)
	if got := APIError(b, "403 Forbidden"); got != "Must supply an API key! Check your request and try again." {
		t.Fatal(got)
	}
}

// Jev on Vercel's and Cloudflare's gateways: known by where it is, named
// as each names it, and a key checked by the gateway's own free call.
func TestDecideGateways(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for id, want := range map[string][2]string{"typesafe": {ViaSystemOne, "jev-latest"}, "bjev": {ViaSystemOne, "bjev"}, "vercel-jev": {ViaVercel, "typesafe-ai/jev"}, "cloudflare-jev": {ViaCloudflare, "typesafe/jev"}} {
		p, err := FromPreset(id)
		if err != nil || !p.Decides() || p.DecideVia() != want[0] || p.Jev() != want[1] || p.decideModels()[0].ID != want[1] {
			t.Errorf("%s: %+v %v", id, p, err)
		}
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			if strings.HasPrefix(r.URL.Path, "/client/") {
				http.Error(w, `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`, 403)
			} else {
				http.Error(w, `{"error":{"message":"Invalid API key"}}`, 401)
			}
			return
		}
		switch r.URL.Path {
		case "/v1/credits":
			w.Write([]byte(`{"balance":"5.00","total_used":"0.00"}`))
		case "/client/v4/accounts":
			w.Write([]byte(`{"success":true,"result":[{"id":"acc9"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	for _, c := range []struct{ decide, bad string }{{up.URL + "/v4/ai", "Invalid API key"}, {up.URL + "/client/v4/", "Authentication error"}} {
		p := Provider{ID: "g", Name: "G", Key: "good", Decide: c.decide}
		if r := p.Test(context.Background()); len(r) != 1 || !r[0].OK || r[0].Model != p.Jev() {
			t.Errorf("%s: %+v", c.decide, r)
		}
		p.Key = "bad"
		if r := p.Test(context.Background()); len(r) != 1 || r[0].OK || !strings.Contains(r[0].Error, c.bad) {
			t.Errorf("%s bad key: %+v", c.decide, r)
		}
	}
	p := Provider{ID: "g", Name: "G", Key: "good", Decide: up.URL + "/client/v4"}
	if u, err := p.DecideURL(context.Background()); err != nil || u != up.URL+"/client/v4/accounts/acc9/ai/run" {
		t.Fatalf("%s %v", u, err)
	}
}

// bjev is a keyless System One service with an OpenAI-compatible model list.
func TestBjev(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("unexpected authorization %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object":"list","data":[{"id":"bjev","object":"model","owned_by":"rayless"}]}`))
	}))
	defer up.Close()
	p, err := FromPreset("bjev")
	if err != nil || !p.Ready() || p.Jev() != "bjev" {
		t.Fatalf("preset %+v %v", p, err)
	}
	p.Decide = up.URL + "/v1"
	ms, err := p.Fetch(context.Background())
	if err != nil || len(ms) != 1 || ms[0].ID != "bjev" {
		t.Fatalf("models %+v %v", ms, err)
	}
	if u, err := p.DecideURL(context.Background()); err != nil || u != up.URL+"/v1/systemone" {
		t.Fatalf("url %q %v", u, err)
	}
}
