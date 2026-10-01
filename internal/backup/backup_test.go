package backup

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/library"
	"github.com/yetone/magpie/internal/profile"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// home gives the test a machine of its own: no agents, no magpie files.
func home(t *testing.T) {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h, ".cache"))
	t.Setenv("PATH", h)
	for _, v := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "DSH_HOME", "GEMINI_CLI_HOME", "OPENCODE_CONFIG",
		"PI_CODING_AGENT_DIR", "COPILOT_HOME", "CLINE_DIR", "GROK_HOME", "HERMES_HOME", "HANA_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(v, "")
	}
}

func setUp(t *testing.T) {
	t.Helper()
	icon, err := provider.StoreIcon([]byte("\x89PNG\r\n\x1a\n0000000000000000"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []provider.Provider{
		{ID: "acme", Name: "Acme", Chat: "https://acme.example.com/v1", Key: "sk-acme", Icon: icon,
			Keys:    []provider.KeyAccount{{Name: "second", Key: "sk-acme-2"}},
			Headers: map[string]string{"X-Team": "a", "X-Api-Key": "hdr-secret"}},
		{ID: "beta", Name: "Beta", Chat: "https://beta.example.com/v1", Key: "sk-beta"},
	} {
		if err := provider.Save(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := settings.Save(settings.Settings{Theme: "dark", Lang: "zh"}); err != nil {
		t.Fatal(err)
	}
	if err := profile.Save("work", profile.Profile{Fields: map[string]string{"claude.model": "acme/m1"}}); err != nil {
		t.Fatal(err)
	}
}

func TestRoundTrip(t *testing.T) {
	home(t)
	setUp(t)
	b, err := Collect(true, "test")
	if err != nil {
		t.Fatal(err)
	}
	original, err := provider.Find("acme")
	if err != nil {
		t.Fatal(err)
	}
	data, err := Seal(b, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"sk-acme", "hdr-secret", "acme.example.com", "work"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("%q readable in the file", secret)
		}
	}
	if _, err := Open(data, "wrong"); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}

	home(t) // another machine
	got, err := Open(data, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Restore(got, All)
	if err != nil {
		t.Fatal(err)
	}
	if r.Added != 2 || !r.Settings || r.Profiles != 1 || len(r.NeedKey) != 0 {
		t.Fatalf("result: %+v", r)
	}
	p, err := provider.Find("acme")
	if err != nil || p.Key != "sk-acme" || p.Headers["X-Api-Key"] != "hdr-secret" {
		t.Fatalf("acme: %+v %v", p, err)
	}
	if !slices.EqualFunc(p.Keys, original.Keys, func(a, b provider.KeyAccount) bool {
		return a.ID == b.ID && a.Key == b.Key && a.Name == b.Name && a.Protocol == b.Protocol && a.Off == b.Off
	}) || p.KeyID != original.KeyID {
		t.Fatalf("credential identity, order or selection changed: before %+v, after %+v", original.Keys, p.Keys)
	}
	name, _ := strings.CutPrefix(p.Icon, "file:")
	if _, err := os.Stat(provider.IconFile(name)); err != nil {
		t.Fatalf("icon: %v", err)
	}
	if s := settings.Load(); s.Theme != "dark" || s.Lang != "zh" {
		t.Fatalf("settings: %+v", s)
	}
	if ps, _ := profile.Load(); ps["work"].Fields["claude.model"] != "acme/m1" {
		t.Fatalf("profiles: %+v", ps)
	}
}

func TestLinkedUsageRoundTrip(t *testing.T) {
	home(t)
	source := provider.UsageSource{ID: "usage", Name: "Usage", Type: "sub2api", BaseURL: "https://usage.example", Credential: "source-secret"}
	pool := provider.QuotaPool{ID: "shared", Name: "Shared", SourceRef: source.ID, AccountIDs: []string{"42"}}
	secondPool := provider.QuotaPool{ID: "second", Name: "Second", SourceRef: source.ID, AccountIDs: []string{"43"}}
	p := provider.Provider{ID: "linked", Name: "Linked", Chat: "https://api.example/v1", Keys: []provider.KeyAccount{{ID: "stable", Key: "key-secret", PoolRefs: []string{pool.ID, secondPool.ID}, Models: []string{"model"}}}}
	if _, _, err := provider.RestoreConfiguration([]provider.Provider{p}, nil, []provider.UsageSource{source}, []provider.QuotaPool{pool, secondPool}); err != nil {
		t.Fatal(err)
	}
	b, err := Collect(true, "test")
	if err != nil {
		t.Fatal(err)
	}
	data, err := Seal(b, "passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), source.Credential) {
		t.Fatal("source credential readable")
	}
	home(t)
	b, err = Open(data, "passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Restore(b, Parts{Providers: true}); err != nil {
		t.Fatal(err)
	}
	if ss := provider.UsageSources(); len(ss) != 1 || ss[0].Credential != source.Credential {
		t.Fatalf("sources: %+v", ss)
	}
	if pools := provider.QuotaPools(); len(pools) != 2 || pools[0].SourceRef != source.ID || !slices.Equal(pools[0].AccountIDs, pool.AccountIDs) || !slices.Equal(pools[1].AccountIDs, secondPool.AccountIDs) {
		t.Fatalf("pools: %+v", pools)
	}
	got, err := provider.Find(p.ID)
	if err != nil || len(got.Keys) != 1 || got.Keys[0].ID != "stable" || !slices.Equal(got.Keys[0].PoolRefs, p.Keys[0].PoolRefs) {
		t.Fatalf("linked provider: %+v %v", got, err)
	}
	keyless, err := Collect(false, "test")
	if err != nil {
		t.Fatal(err)
	}
	if keyless.Sources[0].Credential != "" || keyless.Providers[0].Keys[0].Key != "" || !slices.Equal(keyless.Providers[0].Keys[0].PoolRefs, p.Keys[0].PoolRefs) {
		t.Fatalf("keyless: %+v", keyless)
	}
	if _, err = Restore(keyless, Parts{Providers: true}); err != nil {
		t.Fatal(err)
	}
	got, _ = provider.Find(p.ID)
	if got.Keys[0].Key != "key-secret" || provider.UsageSources()[0].Credential != source.Credential {
		t.Fatal("keyless restore lost existing credentials")
	}
	home(t)
	data, err = Seal(keyless, "passphrase")
	if err != nil {
		t.Fatal(err)
	}
	keyless, err = Open(data, "passphrase")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Restore(keyless, Parts{Providers: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err = provider.Find(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Ready() || got.Key != "" || got.KeyID != "" || got.Keys[0].Key != "" || provider.UsageSources()[0].Credential != "" || !slices.Equal(r.NeedKey, []string{"Linked"}) {
		t.Fatalf("fresh keyless restore acquired credentials: %+v %+v", got, r)
	}
	if got.Keys[0].ID != "stable" || !slices.Equal(got.Keys[0].PoolRefs, p.Keys[0].PoolRefs) || !slices.Equal(got.Keys[0].Models, p.Keys[0].Models) {
		t.Fatalf("fresh keyless restore lost bindings: %+v", got.Keys)
	}
}

func TestRestoreRejectsMissingBoundPool(t *testing.T) {
	home(t)
	source := provider.UsageSource{ID: "usage", Name: "Usage", Type: "sub2api", BaseURL: "https://usage.example", Credential: "source-secret"}
	pool := provider.QuotaPool{ID: "shared", Name: "Shared", SourceRef: source.ID, AccountIDs: []string{"42"}}
	p := provider.Provider{ID: "linked", Name: "Linked", Chat: "https://api.example/v1", Keys: []provider.KeyAccount{{ID: "stable", Key: "key-secret", PoolRefs: []string{pool.ID, "missing"}}}}
	if _, _, err := provider.RestoreConfiguration([]provider.Provider{p}, nil, []provider.UsageSource{source}, []provider.QuotaPool{pool}); err == nil {
		t.Fatal("restore accepted a missing second bound pool")
	}
	if len(provider.All()) != 0 || len(provider.UsageSources()) != 0 || len(provider.QuotaPools()) != 0 {
		t.Fatal("rejected restore persisted partial configuration")
	}
}

// Without keys, nothing secret leaves; restored over a machine that has the
// provider, its keys there stay, and one new there is named as needing a key.
func TestNoKeys(t *testing.T) {
	home(t)
	setUp(t)
	b, err := Collect(false, "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range b.Providers {
		if p.Key != "" || slices.ContainsFunc(p.Keys, func(k provider.KeyAccount) bool { return k.Key != "" }) {
			t.Fatalf("key in a keyless backup: %+v", p)
		}
		if _, ok := p.Headers["X-Api-Key"]; ok {
			t.Fatalf("auth header in a keyless backup: %+v", p.Headers)
		}
	}
	data, _ := Seal(b, "pw")

	home(t)
	oldKeyID := b.Providers[0].Keys[0].ID
	if err := provider.Save(provider.Provider{ID: "acme", Name: "Acme old", Chat: "https://old.example.com/v1", Keys: []provider.KeyAccount{{ID: oldKeyID, Key: "sk-here"}}}); err != nil {
		t.Fatal(err)
	}
	got, err := Open(data, "pw")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Restore(got, Parts{Providers: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Added != 1 || r.Replaced != 1 || !slices.Equal(r.NeedKey, []string{"Beta"}) || r.Settings || r.Profiles != 0 {
		t.Fatalf("result: %+v", r)
	}
	if p, _ := provider.Find("acme"); p.Key != "sk-here" || p.Chat != "https://acme.example.com/v1" || p.Headers["X-Team"] != "a" {
		t.Fatalf("acme: %+v", p)
	}
}

// The library goes too: its sets, servers and skills' files; without keys
// a server's secret-looking values stay behind, and the ones on the
// machine restored to stay. A backup from before the library leaves it.
func TestLibrary(t *testing.T) {
	home(t)
	text := "Be brief."
	if _, err := library.SaveInstructions(library.InstructionsChange{Shared: &text}); err != nil {
		t.Fatal(err)
	}
	srv := library.Server{Name: "gh", Transport: "http", URL: "https://mcp.example.com",
		Headers: map[string]string{"Authorization": "Bearer lib-secret", "X-Org": "acme"}, Agents: []string{}}
	if _, err := library.SaveServer("", srv); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(os.Getenv("HOME"), "skills", "notes")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: notes\ndescription: Notes\n---\n"), 0o644)
	if _, err := library.InstallSkills(dir, []string{""}, nil); err != nil {
		t.Fatal(err)
	}
	full, err := Collect(true, "test")
	if err != nil || full.Library == nil || full.Library.MCP[0].Headers["Authorization"] != "Bearer lib-secret" {
		t.Fatalf("with keys: %+v %v", full.Library, err)
	}
	b, _ := Collect(false, "test")
	if h := b.Library.MCP[0].Headers; h["Authorization"] != "" || h["X-Org"] != "acme" {
		t.Fatalf("without keys: %v", h)
	}
	data, _ := Seal(b, "pw")
	older, _ := Seal(Bundle{Version: 1, Providers: []provider.Provider{}}, "pw")

	home(t) // another machine, with the server and its key already
	srv.Headers = map[string]string{"Authorization": "Bearer here", "X-Org": "old"}
	if _, err := library.SaveServer("", srv); err != nil {
		t.Fatal(err)
	}
	got, _ := Open(older, "pw")
	if r, err := Restore(got, All); err != nil || r.Library {
		t.Fatalf("an older backup: %+v %v", r, err)
	}
	got, err = Open(data, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if r, err := Restore(got, Parts{Library: false, Settings: true}); err != nil || r.Library {
		t.Fatalf("library not picked: %+v %v", r, err)
	}
	if l, _ := library.Collect(); l.Texts["default"] != "" {
		t.Fatal("the library came in unpicked")
	}
	r, err := Restore(got, All)
	if err != nil || !r.Library {
		t.Fatalf("restore: %+v %v", r, err)
	}
	l, _ := library.Collect()
	if l.Texts["default"] != "Be brief." || len(l.Skills) != 1 || l.Skills[0].Name != "notes" || len(l.Skills[0].Files) != 1 {
		t.Fatalf("library: %+v", l)
	}
	if h := l.MCP[0].Headers; h["Authorization"] != "Bearer here" || h["X-Org"] != "acme" {
		t.Fatalf("server: %v", h)
	}
}

func TestNotABackup(t *testing.T) {
	for _, data := range []string{"", "{}", `{"format":"magpie-backup","version":9,"kdf":"x"}`} {
		if _, err := Open([]byte(data), "pw"); err == nil || errors.Is(err, ErrPassphrase) {
			t.Fatalf("%q: %v", data, err)
		}
	}
	if _, err := Seal(Bundle{}, ""); err == nil {
		t.Fatal("sealed with no passphrase")
	}
}

func TestTampered(t *testing.T) {
	data, err := Seal(Bundle{Version: 1}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	// lowering the work of the key derivation is noticed
	bad := strings.Replace(string(data), `"iterations": 600000`, `"iterations": 100000`, 1)
	if _, err := Open([]byte(bad), "pw"); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("tampered header: %v", err)
	}
}

func TestProfileKeys(t *testing.T) {
	got := profileKeys(map[string]string{"codex.effort": "", "claude.model": "", "codex.provider": "", "codex.model": ""})
	want := []string{"codex.provider", "claude.model", "codex.model", "codex.effort"}
	if !slices.Equal(got, want) {
		t.Fatalf("%v", got)
	}
}
