package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

const quotaBindingsUsage = `usage: magpie quota sources <list|add|edit|remove|discover|import-env> [flags]
       magpie quota pools <list|add|edit|remove> [flags]
Sources: --id --name --type --base-url --credential --credential-env --env-file --auth-index --project --off
         --weekly-model --load-model --command (Traex collection configuration)
Types: sub2api, google-proxy, glm, deepseek, traex.
Pools: --id --name --source <source-id> --accounts <upstream-id,...>
Edit preserves omitted flags; use --off=false to enable a source.
List prints JSON with source credentials masked. Discover requires --id.
Import-env explicitly imports SUB2API_BASE_URL/SUB2API_ADMIN_API_KEY,
CPAMC_BASE_URL/CPAMC_TOKEN/CPAMC_GOOGLE_AUTH_INDEX, ZHIPU_API_KEY and
DEEPSEEK_API_KEY into sources only; it never binds keys or creates pools.
A pool conservatively uses the maximum applicable usage across its accounts
for each window. Missing usage is unknown, not zero. Pools do not select an
upstream account. Bind keys explicitly with the keys CLI.`

func quotaBindingsCmd(args []string) error {
	if len(args) < 2 || args[1] == "help" || args[1] == "--help" || args[1] == "-h" {
		fmt.Println(quotaBindingsUsage)
		return nil
	}
	if err := provider.CheckUsageConfiguration(); err != nil {
		return err
	}
	kind, action := args[0], args[1]
	fs := flag.NewFlagSet("quota "+kind+" "+action, flag.ContinueOnError)
	fs.Usage = func() { fmt.Println(quotaBindingsUsage) }
	id := fs.String("id", "", "stable ID")
	name := fs.String("name", "", "display name")
	var sourceType, baseURL, credential, credentialEnv, envFile, authIndex, source, accounts string
	var weeklyModel, loadModel, command string
	var project string
	var off bool
	if kind == "sources" {
		fs.StringVar(&sourceType, "type", "", "source type")
		fs.StringVar(&baseURL, "base-url", "", "source base URL")
		fs.StringVar(&credential, "credential", "", "source credential")
		fs.StringVar(&credentialEnv, "credential-env", "", "credential environment variable")
		fs.StringVar(&envFile, "env-file", "", "credential environment file")
		fs.StringVar(&authIndex, "auth-index", "", "Google proxy auth index")
		fs.StringVar(&project, "project", "", "Google proxy project")
		fs.StringVar(&weeklyModel, "weekly-model", "", "Traex weekly usage model")
		fs.StringVar(&loadModel, "load-model", "", "Traex load model")
		fs.StringVar(&command, "command", "", "Traex collection command")
		fs.BoolVar(&off, "off", false, "disable source")
	} else {
		fs.StringVar(&source, "source", "", "source ID")
		fs.StringVar(&accounts, "accounts", "", "comma-separated upstream account IDs")
	}
	if err := fs.Parse(args[2:]); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	seen := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	printJSON := func(v any) error {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	if action == "list" {
		if kind == "sources" {
			return printJSON(provider.PublicUsageSources())
		}
		return printJSON(provider.QuotaPools())
	}
	if kind == "sources" && action == "import-env" {
		return quotaImportEnv()
	}
	if *id == "" {
		return fmt.Errorf("--id is required\n%s", quotaBindingsUsage)
	}
	if kind == "sources" {
		s := provider.UsageSource{ID: *id}
		found := false
		for _, existing := range provider.UsageSources() {
			if existing.ID == *id {
				s, found = existing, true
				break
			}
		}
		switch action {
		case "remove":
			return provider.DeleteUsageSource(*id)
		case "discover":
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, err := provider.DiscoverUsageSource(ctx, *id)
			if err != nil {
				return err
			}
			return printJSON(result)
		case "add":
			if found {
				return fmt.Errorf("source %q already exists", *id)
			}
		case "edit":
			if !found {
				return fmt.Errorf("source %q not found", *id)
			}
		default:
			return fmt.Errorf("unknown sources command %q", action)
		}
		if seen["name"] {
			s.Name = *name
		}
		if seen["type"] {
			s.Type = sourceType
		}
		if seen["base-url"] {
			s.BaseURL = baseURL
		}
		if seen["credential"] {
			s.Credential = credential
		}
		if seen["credential-env"] {
			s.CredentialEnv = credentialEnv
		}
		if seen["env-file"] {
			s.EnvFile = envFile
		}
		if seen["auth-index"] {
			s.AuthIndex = authIndex
		}
		if seen["project"] {
			s.Project = project
		}
		if seen["weekly-model"] {
			s.WeeklyModel = weeklyModel
		}
		if seen["load-model"] {
			s.LoadModel = loadModel
		}
		if seen["command"] {
			s.Command = command
		}
		if seen["off"] {
			s.Off = off
		}
		switch s.Type {
		case "sub2api", "google-proxy", "glm", "deepseek", "traex":
		default:
			return fmt.Errorf("unsupported source type %q", s.Type)
		}
		return provider.SaveUsageSource(s)
	}
	p := provider.QuotaPool{ID: *id}
	found := false
	for _, existing := range provider.QuotaPools() {
		if existing.ID == *id {
			p, found = existing, true
			break
		}
	}
	switch action {
	case "remove":
		return provider.DeleteQuotaPool(*id)
	case "add":
		if found {
			return fmt.Errorf("pool %q already exists", *id)
		}
	case "edit":
		if !found {
			return fmt.Errorf("pool %q not found", *id)
		}
	default:
		return fmt.Errorf("unknown pools command %q", action)
	}
	if seen["name"] {
		p.Name = *name
	}
	if seen["source"] {
		p.SourceRef = source
	}
	if seen["accounts"] {
		p.AccountIDs = nil
		for _, account := range strings.Split(accounts, ",") {
			if account = strings.TrimSpace(account); account != "" {
				p.AccountIDs = append(p.AccountIDs, account)
			}
		}
	}
	return provider.SaveQuotaPool(p)
}

func quotaImportEnv() error {
	sources := []provider.UsageSource{
		{ID: "legacy-sub2api", Name: "Imported sub2api", Type: "sub2api", BaseURL: os.Getenv("SUB2API_BASE_URL"), Credential: os.Getenv("SUB2API_ADMIN_API_KEY")},
		{ID: "legacy-google-proxy", Name: "Imported Google proxy", Type: "google-proxy", BaseURL: os.Getenv("CPAMC_BASE_URL"), Credential: os.Getenv("CPAMC_TOKEN"), AuthIndex: os.Getenv("CPAMC_GOOGLE_AUTH_INDEX")},
		{ID: "legacy-glm", Name: "Imported GLM", Type: "glm", Credential: os.Getenv("ZHIPU_API_KEY")},
		{ID: "legacy-deepseek", Name: "Imported DeepSeek", Type: "deepseek", Credential: os.Getenv("DEEPSEEK_API_KEY")},
	}
	existing := make(map[string]bool)
	for _, s := range provider.UsageSources() {
		existing[s.ID] = true
	}
	for _, s := range sources {
		if s.Credential == "" && s.BaseURL == "" && s.AuthIndex == "" {
			continue
		}
		if existing[s.ID] {
			return fmt.Errorf("source %q already exists; edit it explicitly instead of overwriting", s.ID)
		}
	}
	for _, s := range sources {
		if s.Credential == "" && s.BaseURL == "" && s.AuthIndex == "" {
			continue
		}
		if err := provider.SaveUsageSource(s); err != nil {
			return err
		}
		fmt.Printf("Imported source %s\n", s.ID)
	}
	return nil
}
