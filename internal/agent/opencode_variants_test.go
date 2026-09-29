package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
)

func TestOpenCodeVariants(t *testing.T) {
	for _, c := range []struct {
		efforts []string
		want    map[string]any
	}{
		{[]string{"none", "high", "max"}, map[string]any{
			"none": map[string]any{"reasoningEffort": "none"},
			"high": map[string]any{"reasoningEffort": "high"},
			"max":  map[string]any{"reasoningEffort": "max"},
		}},
		{[]string{"low", "medium", "high", "xhigh"}, map[string]any{
			"low":    map[string]any{"reasoningEffort": "low"},
			"medium": map[string]any{"reasoningEffort": "medium"},
			"high":   map[string]any{"reasoningEffort": "high"},
			"xhigh":  map[string]any{"reasoningEffort": "xhigh"},
		}},
		// none at all: an empty set, so OpenCode 2 doesn't make low, medium
		// and high of its own
		{nil, map[string]any{}},
	} {
		if got := openCodeVariants(c.efforts); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: got %v, want %v", c.efforts, got, c.want)
		}
	}
}

// TestOpenCodeWritesVariants: each model of magpie's in opencode.json names
// the reasoning levels it has as variants — OpenCode 2 otherwise offers low,
// medium and high for every one — and the rest of the entry is as before.
func TestOpenCodeWritesVariants(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	if err := provider.Save(provider.Provider{
		ID: "think", Name: "Think", Chat: "https://example.test/v1", Key: "key",
		Models: []string{"deep", "wide", "plain"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveLive("think", "https://example.test/v1", []catalog.Model{
		{ID: "deep", Efforts: []string{"none", "high", "max"}, Context: 1000000, Output: 64000},
		{ID: "wide", Efforts: []string{"low", "medium", "high", "xhigh", "max"}},
		{ID: "plain"},
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	writeFile(t, path, `{"theme":"dark"}`)
	if err := opencode(home, filepath.Join(home, ".config")).Field("model").Set("magpie/think/deep"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Theme    string `json:"theme"`
		Model    string `json:"model"`
		Provider map[string]struct {
			NPM    string                    `json:"npm"`
			Models map[string]map[string]any `json:"models"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Theme != "dark" || cfg.Model != "magpie/think/deep" {
		t.Fatalf("config: %s", b)
	}
	p := cfg.Provider["magpie"]
	if p.NPM != "@ai-sdk/openai-compatible" {
		t.Fatalf("npm %q", p.NPM)
	}
	levels := func(id string) []string {
		t.Helper()
		m, ok := p.Models[id]
		if !ok {
			t.Fatalf("%s missing: %s", id, b)
		}
		vs, ok := m["variants"].(map[string]any)
		if !ok {
			t.Fatalf("%s has no variants: %v", id, m)
		}
		var out []string
		for _, e := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
			if v, ok := vs[e]; ok {
				if !reflect.DeepEqual(v, map[string]any{"reasoningEffort": e}) {
					t.Fatalf("%s %s: %v", id, e, v)
				}
				out = append(out, e)
			}
		}
		if len(out) != len(vs) {
			t.Fatalf("%s: unexpected variants %v", id, vs)
		}
		return out
	}
	if got := levels("think/deep"); !reflect.DeepEqual(got, []string{"none", "high", "max"}) {
		t.Fatalf("deep: %v", got)
	}
	if got := levels("think/wide"); !reflect.DeepEqual(got, []string{"low", "medium", "high", "xhigh", "max"}) {
		t.Fatalf("wide: %v", got)
	}
	if got := levels("think/plain"); got != nil {
		t.Fatalf("plain: %v", got)
	}
	if lim, _ := p.Models["think/deep"]["limit"].(map[string]any); lim["context"] != float64(1000000) {
		t.Fatalf("deep limit: %v", p.Models["think/deep"])
	}
}
