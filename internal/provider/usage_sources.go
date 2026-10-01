package provider

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// UsageSource is an explicitly configured quota endpoint. Secrets follow the
// same mode-0600 storage convention as provider keys, or may be resolved from
// a named environment variable in a non-executable KEY=VALUE file.
type UsageSource struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	BaseURL       string `json:"baseURL,omitempty"`
	Credential    string `json:"credential,omitempty"`
	CredentialEnv string `json:"credentialEnv,omitempty"`
	EnvFile       string `json:"envFile,omitempty"`
	AuthIndex     string `json:"authIndex,omitempty"`
	Project       string `json:"project,omitempty"`
	Off           bool   `json:"off,omitempty"`
	WeeklyModel   string `json:"weeklyModel,omitempty"`
	LoadModel     string `json:"loadModel,omitempty"`
	Command       string `json:"command,omitempty"`
}

// QuotaPool groups upstream account IDs, not names. Requests select API keys,
// not the accounts behind a pool; its fullest applicable window is conservative.
// Account IDs are opaque in configuration; each source validates them when
// discovering or collecting usage. An unselected account stays unknown.
type QuotaPool struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	SourceRef  string   `json:"sourceRef"`
	AccountIDs []string `json:"accountIDs,omitempty"`
}

func usageConfig() (file, error) {
	var f file
	b, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err = json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("read providers configuration: %w", err)
	}
	return f, nil
}

func UsageSources() []UsageSource { return load().Sources }
func QuotaPools() []QuotaPool     { return load().QuotaPools }

// CheckUsageConfiguration lets public configuration consumers report malformed
// or unreadable persisted files rather than presenting an empty configuration.
func CheckUsageConfiguration() error { _, err := usageConfig(); return err }
func PublicUsageSources() []UsageSource {
	ss := UsageSources()
	for i := range ss {
		ss[i].Credential = Mask(ss[i].Credential)
	}
	return ss
}

func SourceCredential(s UsageSource) (string, error) {
	if s.Credential != "" && s.CredentialEnv != "" {
		return "", errors.New("credential and credentialEnv are mutually exclusive")
	}
	if s.Credential != "" {
		return s.Credential, nil
	}
	if s.CredentialEnv == "" {
		return "", errors.New("source credential is missing")
	}
	if s.EnvFile == "" {
		v := os.Getenv(s.CredentialEnv)
		if v == "" {
			return "", fmt.Errorf("credential environment variable %s is empty", s.CredentialEnv)
		}
		return v, nil
	}
	path := os.ExpandEnv(s.EnvFile)
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(Path()), path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open credential environment file: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	values := map[string]string{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" || strings.ContainsAny(k, " \t;()$") {
			return "", errors.New("credential environment file must contain only KEY=VALUE entries")
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		values[k] = v
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if values[s.CredentialEnv] == "" {
		return "", fmt.Errorf("credential environment file has no value for %s", s.CredentialEnv)
	}
	return values[s.CredentialEnv], nil
}

func validateSource(s UsageSource) error {
	return validateSourceFields(s, true)
}

func validateSourceFields(s UsageSource, requireCredential bool) error {
	if s.ID == "" || Slug(s.ID) != s.ID {
		return errors.New("source id must be a nonempty lowercase slug")
	}
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("source name is required")
	}
	switch s.Type {
	case "sub2api", "google-proxy", "glm", "deepseek", "traex":
	default:
		return errors.New("unsupported usage source type")
	}
	if s.Credential != "" && s.CredentialEnv != "" {
		return errors.New("credential and credentialEnv are mutually exclusive")
	}
	if s.EnvFile != "" && s.CredentialEnv == "" {
		return errors.New("envFile requires credentialEnv")
	}
	if requireCredential && s.Credential == "" && s.CredentialEnv == "" && s.Type != "traex" {
		return errors.New("source credential or credentialEnv is required")
	}
	if s.CredentialEnv != "" && strings.ContainsAny(s.CredentialEnv, " \t\r\n=$;()") {
		return errors.New("invalid credentialEnv name")
	}
	if s.BaseURL == "" {
		if s.Type == "sub2api" || s.Type == "google-proxy" {
			return errors.New("source baseURL is required")
		}
		return nil
	}
	u, err := url.Parse(s.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("source baseURL must be an HTTP(S) URL without credentials, query, or fragment")
	}
	return nil
}

func validatePool(p QuotaPool, sources []UsageSource) error {
	if p.ID == "" || Slug(p.ID) != p.ID {
		return errors.New("pool id must be a nonempty lowercase slug")
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("pool name is required")
	}
	i := slices.IndexFunc(sources, func(s UsageSource) bool { return s.ID == p.SourceRef })
	if i < 0 {
		return fmt.Errorf("pool source %q does not exist", p.SourceRef)
	}
	seen := map[string]bool{}
	for _, id := range p.AccountIDs {
		if strings.TrimSpace(id) == "" || seen[id] {
			return errors.New("account IDs must be nonempty and unique")
		}
		seen[id] = true
	}
	return nil
}

func SaveUsageSource(s UsageSource) error {
	s.Name = strings.TrimSpace(s.Name)
	s.BaseURL = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	if err := validateSource(s); err != nil {
		return err
	}
	f, err := usageConfig()
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(f.Sources, func(x UsageSource) bool { return x.ID == s.ID }); i >= 0 {
		f.Sources[i] = s
	} else {
		f.Sources = append(f.Sources, s)
	}
	for _, p := range f.QuotaPools {
		if err := validatePool(p, f.Sources); err != nil {
			return err
		}
	}
	return store(f)
}
func DeleteUsageSource(id string) error {
	f, err := usageConfig()
	if err != nil {
		return err
	}
	for _, p := range f.QuotaPools {
		if p.SourceRef == id {
			return fmt.Errorf("source is referenced by pool %s", p.ID)
		}
	}
	i := slices.IndexFunc(f.Sources, func(s UsageSource) bool { return s.ID == id })
	if i < 0 {
		return errors.New("source not found")
	}
	f.Sources = slices.Delete(f.Sources, i, i+1)
	return store(f)
}
func SaveQuotaPool(p QuotaPool) error {
	p.Name = strings.TrimSpace(p.Name)
	f, err := usageConfig()
	if err != nil {
		return err
	}
	if err := validatePool(p, f.Sources); err != nil {
		return err
	}
	if i := slices.IndexFunc(f.QuotaPools, func(x QuotaPool) bool { return x.ID == p.ID }); i >= 0 {
		f.QuotaPools[i] = p
	} else {
		f.QuotaPools = append(f.QuotaPools, p)
	}
	return store(f)
}
func DeleteQuotaPool(id string) error {
	f, err := usageConfig()
	if err != nil {
		return err
	}
	for _, p := range f.Providers {
		for _, k := range p.Keys {
			if slices.Contains(k.PoolRefs, id) {
				return fmt.Errorf("pool is referenced by key %s of provider %s", k.ID, p.ID)
			}
		}
	}
	i := slices.IndexFunc(f.QuotaPools, func(p QuotaPool) bool { return p.ID == id })
	if i < 0 {
		return errors.New("pool not found")
	}
	f.QuotaPools = slices.Delete(f.QuotaPools, i, i+1)
	return store(f)
}
