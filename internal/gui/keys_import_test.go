package gui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

// keys/import adds the keys pasted at once (361 on Discord), says how many
// were new, and a key the gateway rests says so in its row.
func TestKeysImportAndRest(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h, ".cache"))
	p := provider.Provider{ID: "import-test", Name: "Import", Chat: "https://example.invalid/v1", Key: "first"}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(10 * time.Minute)
	// keys/import API goes through provider.Save which calls normalizeKeys
	// to assign stable IDs — we need those real IDs, not placeholders.
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/keys/import", strings.NewReader(body)))
		return w
	}
	w := post(`{"id":"import-test","key":"first\ntwo, three\n\n'four'"}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var imported providersJSON
	if err := json.Unmarshal(w.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Added != 3 || imported.Had != 1 {
		t.Fatalf("added %d had %d", imported.Added, imported.Had)
	}
	// Now read the actual persisted IDs to match against RestKey.
	fixture, err := provider.Find("import-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.Keys) != 4 {
		t.Fatalf("fixture should have 4 keys after import, got %d: %v", len(fixture.Keys), fixture.Keys)
	}
	// The key whose secret is "two" — in normalizeKeys order after import,
	// fixture.Keys[i].Key == "two"; build its canonical RestKey = id + "#" + KeyID.
	var twoKeyID string
	for _, k := range fixture.Keys {
		if k.Key == "two" {
			twoKeyID = k.ID
			break
		}
	}
	if twoKeyID == "" {
		t.Fatalf("could not find 'two' key in fixture: %v", fixture.Keys)
	}

	// The vendor's words go with the rest under the key's stable ID.
	said := "Your credit balance is too low to access the API"
	was := keyRestOf
	keyRestOf = func(key string) (gateway.Rest, bool) {
		if key == p.ID+"#"+twoKeyID {
			return gateway.Rest{Why: "credit", Status: 400, Until: until, By: "credit", Error: said}, true
		}
		return gateway.Rest{}, false
	}
	defer func() { keyRestOf = was }()

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/providers", nil))

	var st providersJSON
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	var info *providerJSON
	for i := range st.Providers {
		if st.Providers[i].ID == "import-test" {
			info = &st.Providers[i]
		}
	}
	if info == nil || len(info.KeyList) != 4 {
		t.Fatalf("keys: %+v", info)
	}
	// The second key (index 1) in display order has Rest != nil because
	// RestKey on the imported key whose secret is "two" matches our mock.
	twoDisplayIdx := -1
	for i, k := range info.KeyList {
		if k.ID == twoKeyID {
			twoDisplayIdx = i
			break
		}
	}
	if twoDisplayIdx < 0 {
		t.Fatalf("could not find 'two' key in response KeyList: %+v", info.KeyList)
	}
	for i, k := range info.KeyList {
		if (k.Rest != nil) != (i == twoDisplayIdx) {
			t.Fatalf("key %d rest %+v", i, k.Rest)
		}
	}
	if r := info.KeyList[twoDisplayIdx].Rest; r.Status != 400 || r.Why != "credit" || !r.Until.Equal(until) || r.Error != said || r.By != "credit" {
		t.Fatalf("rest %+v", r)
	}
	for _, k := range []string{`"first"`, `"two"`, `"three"`, `"four"`} {
		if strings.Contains(w.Body.String(), `"key":`+k) {
			t.Fatal("leaked a key")
		}
	}
	// pasting only keys it has is refused, saying so
	w = post(`{"id":"import-test","key":"two three"}`)
	if w.Code == 200 || !strings.Contains(w.Body.String(), "already has") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// @YsiCing on X: a key the gateway rested showed "暂停使用 · 400 余额不足 ·
// 13:37 恢复" and nothing of what its vendor answered, where an account's
// row shows it. A real refusal, from a fake upstream through the gateway,
// reaches the key's row in /api/providers with the vendor's words.
func TestKeyRestSaysVendorWords(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h, ".cache"))
	const said = "Your credit balance is too low to access the API. Please go to Plans & Billing to upgrade or purchase credits."
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"`+said+`","type":"invalid_request_error","code":null}}`)
	}))
	defer up.Close()
	p := provider.Provider{ID: "keyrest-words", Name: "Relay", Chat: up.URL + "/v1", Models: []string{"m1"}, Key: "k-one", Keys: []provider.KeyAccount{{Key: "k-two"}}}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	gw := gateway.New()
	prev := served.Swap(gw)
	t.Cleanup(func() { served.Store(prev) })
	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"keyrest-words/m1","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/providers", nil))
	var st providersJSON
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	rested := 0
	for _, pj := range st.Providers {
		if pj.ID != p.ID {
			continue
		}
		for _, k := range pj.KeyList {
			if k.Rest == nil {
				continue
			}
			rested++
			if k.Rest.Status != 400 || k.Rest.Why != "credit" || !strings.Contains(k.Rest.Error, said) {
				t.Fatalf("key %s rest %+v, want its vendor's words", k.Masked, k.Rest)
			}
		}
	}
	// the first key rests; the last one left is rested only on a 429 or
	// 402 (quotaStatus), so a 400 leaves it be
	if rested == 0 {
		t.Fatalf("no key rested: %s", w.Body)
	}
}
