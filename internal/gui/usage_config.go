package gui

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

func usageConfigRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/usage/sources", func(w http.ResponseWriter, r *http.Request) {
		if err := provider.CheckUsageConfiguration(); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, provider.PublicUsageSources())
	})
	saveSource := func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			provider.UsageSource
			ClearCredential bool    `json:"clearCredential"`
			Project         *string `json:"project"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(w, err)
			return
		}
		if id := r.PathValue("id"); id != "" {
			in.ID = id
		}
		if in.Project != nil {
			in.UsageSource.Project = *in.Project
		} else {
			for _, old := range provider.UsageSources() {
				if old.ID == in.ID {
					in.UsageSource.Project = old.Project
					break
				}
			}
		}
		if !in.ClearCredential {
			for _, old := range provider.UsageSources() {
				if old.ID == in.ID && (in.Credential == "" || in.Credential == provider.Mask(old.Credential)) {
					in.Credential = old.Credential
					break
				}
			}
		} else {
			in.Credential = ""
		}
		if err := provider.SaveUsageSource(in.UsageSource); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, provider.PublicUsageSources())
	}
	mux.HandleFunc("POST /api/usage/sources", saveSource)
	mux.HandleFunc("PUT /api/usage/sources/{id}", saveSource)
	mux.HandleFunc("DELETE /api/usage/sources/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := provider.DeleteUsageSource(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, provider.PublicUsageSources())
	})
	mux.HandleFunc("POST /api/usage/sources/{id}/discover", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		accounts, err := provider.DiscoverUsageSource(ctx, r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, accounts)
	})
	mux.HandleFunc("GET /api/usage/pools", func(w http.ResponseWriter, r *http.Request) {
		if err := provider.CheckUsageConfiguration(); err != nil {
			fail(w, err)
			return
		}
		type poolState struct {
			provider.QuotaPool
			Allowance provider.Allowance `json:"allowance"`
			Available bool               `json:"available"`
			Keys      []string           `json:"keys"`
		}
		out := []poolState{}
		for _, pool := range provider.QuotaPools() {
			a, ok := provider.PoolAllowance(pool.ID)
			s := poolState{QuotaPool: pool, Allowance: a, Available: ok, Keys: []string{}}
			for _, p := range provider.All() {
				for _, k := range p.KeyList() {
					if slices.Contains(k.PoolRefs, pool.ID) {
						s.Keys = append(s.Keys, p.Name+" / "+k.Name+" ("+p.ID+" / "+k.ID+")")
					}
				}
			}
			out = append(out, s)
		}
		writeJSON(w, out)
	})
	savePool := func(w http.ResponseWriter, r *http.Request) {
		var in provider.QuotaPool
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(w, err)
			return
		}
		if id := r.PathValue("id"); id != "" {
			in.ID = id
		}
		if err := provider.SaveQuotaPool(in); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, provider.QuotaPools())
	}
	mux.HandleFunc("POST /api/usage/pools", savePool)
	mux.HandleFunc("PUT /api/usage/pools/{id}", savePool)
	mux.HandleFunc("DELETE /api/usage/pools/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := provider.DeleteQuotaPool(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, provider.QuotaPools())
	})
}
