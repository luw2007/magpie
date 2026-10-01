package provider

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func isolateUsageConfiguration(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func usageTestSource() UsageSource {
	return UsageSource{ID: "relay", Name: "Relay", Type: "sub2api", BaseURL: "https://usage.example.com", Credential: "secret-for-usage"}
}

func usageTestPool() QuotaPool {
	return QuotaPool{ID: "team", Name: "Team", SourceRef: "relay", AccountIDs: []string{"account-1", "account-2"}}
}

func TestUsageSourceAndPoolPersistence(t *testing.T) {
	isolateUsageConfiguration(t)
	source, pool := usageTestSource(), usageTestPool()
	if err := SaveUsageSource(source); err != nil {
		t.Fatal(err)
	}
	if err := SaveQuotaPool(pool); err != nil {
		t.Fatal(err)
	}
	if got := UsageSources(); !reflect.DeepEqual(got, []UsageSource{source}) {
		t.Fatalf("persisted sources = %+v, want %+v", got, source)
	}
	if got := QuotaPools(); !reflect.DeepEqual(got, []QuotaPool{pool}) {
		t.Fatalf("persisted pools = %+v, want %+v", got, pool)
	}
	source.Name, source.Credential = "Renamed relay", "rotated-credential"
	pool.Name, pool.AccountIDs = "Renamed team", []string{"account-3"}
	if err := SaveUsageSource(source); err != nil {
		t.Fatal(err)
	}
	if err := SaveQuotaPool(pool); err != nil {
		t.Fatal(err)
	}
	if got := UsageSources(); !reflect.DeepEqual(got, []UsageSource{source}) {
		t.Fatalf("updated sources = %+v", got)
	}
	if got := QuotaPools(); !reflect.DeepEqual(got, []QuotaPool{pool}) {
		t.Fatalf("updated pools = %+v", got)
	}
	info, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential configuration permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestUsagePoolPersistenceAllowsUnselectedAccounts(t *testing.T) {
	isolateUsageConfiguration(t)
	if err := SaveUsageSource(usageTestSource()); err != nil {
		t.Fatal(err)
	}
	pool := usageTestPool()
	pool.AccountIDs = nil
	if err := SaveQuotaPool(pool); err != nil {
		t.Fatal(err)
	}
	if got := QuotaPools(); !reflect.DeepEqual(got, []QuotaPool{pool}) {
		t.Fatalf("unselected pool = %+v, want %+v", got, pool)
	}
}

func TestUsageDeletionProtectsLinkedConfiguration(t *testing.T) {
	isolateUsageConfiguration(t)
	source, pool := usageTestSource(), usageTestPool()
	otherPool := pool
	otherPool.ID = "other-pool"
	if err := store(file{Sources: []UsageSource{source}, QuotaPools: []QuotaPool{otherPool, pool}, Providers: []Provider{{ID: "relay-provider", Keys: []KeyAccount{{ID: "key-1", Key: "api-secret", PoolRefs: []string{otherPool.ID, pool.ID}}}}}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, delete := range []struct {
		name string
		run  func() error
	}{
		{"source linked by pool", func() error { return DeleteUsageSource(source.ID) }},
		{"pool bound to key account", func() error { return DeleteQuotaPool(pool.ID) }},
		{"first pool bound to key account", func() error { return DeleteQuotaPool(otherPool.ID) }},
	} {
		t.Run(delete.name, func(t *testing.T) {
			if err := delete.run(); err == nil {
				t.Fatal("linked deletion succeeded")
			}
			after, err := os.ReadFile(Path())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("rejected deletion changed persisted configuration")
			}
		})
	}
	if err := store(file{Sources: []UsageSource{source}, QuotaPools: []QuotaPool{pool}}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteQuotaPool(pool.ID); err != nil {
		t.Fatal(err)
	}
	if err := DeleteUsageSource(source.ID); err != nil {
		t.Fatal(err)
	}
	if len(UsageSources()) != 0 || len(QuotaPools()) != 0 {
		t.Fatal("unlinked deletion left sources or pools behind")
	}
}

func TestMalformedUsageConfigurationIsNotOverwritten(t *testing.T) {
	for _, mutation := range []struct {
		name string
		run  func() error
	}{
		{"save source", func() error { return SaveUsageSource(usageTestSource()) }},
		{"delete source", func() error { return DeleteUsageSource("relay") }},
		{"save pool", func() error { return SaveQuotaPool(usageTestPool()) }},
		{"delete pool", func() error { return DeleteQuotaPool("team") }},
		{"restore", func() error {
			_, _, err := RestoreConfiguration(nil, nil, []UsageSource{usageTestSource()}, []QuotaPool{usageTestPool()})
			return err
		}},
		{"mirror", func() error {
			return MirrorConfiguration(nil, nil, []UsageSource{usageTestSource()}, []QuotaPool{usageTestPool()})
		}},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			isolateUsageConfiguration(t)
			if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
				t.Fatal(err)
			}
			malformed := []byte(`{"sources":[`)
			if err := os.WriteFile(Path(), malformed, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := mutation.run(); err == nil {
				t.Fatal("mutation accepted malformed persisted configuration")
			}
			got, err := os.ReadFile(Path())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, malformed) {
				t.Fatalf("malformed configuration overwritten: %q", got)
			}
		})
	}
}

func TestSourceCredentialEnvironmentFileIsLiteral(t *testing.T) {
	isolateUsageConfiguration(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "executed")
	literal := "$(touch " + marker + ");$HOME`whoami`"
	path := filepath.Join(dir, "credentials.env")
	if err := os.WriteFile(path, []byte("# credentials\nOTHER=ignored\nTOKEN='"+literal+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOKEN", "process-value-must-not-win")
	got, err := SourceCredential(UsageSource{CredentialEnv: "TOKEN", EnvFile: path})
	if err != nil || got != literal {
		t.Fatalf("credential = %q, %v; want literal %q", got, err, literal)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("credential file executed command: marker stat = %v", err)
	}
	for _, contents := range []string{"set -gx TOKEN secret\n", "TOKEN=secret\nset -gx OTHER value\n"} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := SourceCredential(UsageSource{CredentialEnv: "TOKEN", EnvFile: path}); err == nil {
			t.Fatalf("accepted fish command file %q", contents)
		}
	}
}

func TestPublicUsageSourcesMaskCredentials(t *testing.T) {
	isolateUsageConfiguration(t)
	source := usageTestSource()
	if err := SaveUsageSource(source); err != nil {
		t.Fatal(err)
	}
	public := PublicUsageSources()
	if len(public) != 1 {
		t.Fatalf("public sources = %+v", public)
	}
	if public[0].Credential == "" || strings.Contains(public[0].Credential, source.Credential) {
		t.Fatalf("public credential not masked: %q", public[0].Credential)
	}
	if got := UsageSources(); len(got) != 1 || got[0].Credential != source.Credential {
		t.Fatalf("masking altered stored credential: %+v", got)
	}
}

func TestUsageConfigurationRejectsEmbeddedAuthAndDanglingPool(t *testing.T) {
	isolateUsageConfiguration(t)
	source := usageTestSource()
	source.BaseURL = "https://user:password@usage.example.com"
	if err := SaveUsageSource(source); err == nil {
		t.Fatal("accepted URL containing embedded authentication")
	}
	if err := SaveQuotaPool(usageTestPool()); err == nil {
		t.Fatal("accepted pool referring to absent source")
	}
	if _, err := os.Stat(Path()); !os.IsNotExist(err) {
		t.Fatalf("invalid configuration was persisted: %v", err)
	}
}
