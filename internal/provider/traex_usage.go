package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

// Traex reports model-scoped enterprise windows separately from instantaneous
// shared-slot load. Load is display metadata, never an allowance percentage.
func fetchTraexQuota(ctx context.Context, source UsageSource) (SubscriptionQuota, error) {
	command := source.Command
	command = os.ExpandEnv(command)
	if strings.HasPrefix(command, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return SubscriptionQuota{}, fmt.Errorf("cannot resolve traex executable home")
		}
		command = filepath.Join(home, command[2:])
	}
	if command == "" {
		command = "traex"
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	body, err := proc.CommandContext(ctx, command, "models", "--json").Output()
	if err != nil {
		return SubscriptionQuota{}, fmt.Errorf("traex model discovery failed: %w", err)
	}
	return parseTraexQuota(body, source, time.Now())
}

func parseTraexQuota(body []byte, source UsageSource, now time.Time) (SubscriptionQuota, error) {
	type model struct {
		Name string `json:"name"`
		Meta struct {
			Trae struct {
				Weekly struct {
					Applies *bool    `json:"applies"`
					Used    *float64 `json:"usedPercent"`
					Reset   *float64 `json:"resetTime"`
				} `json:"weeklyQuota"`
				Load struct {
					Percent *float64 `json:"percent"`
				} `json:"load"`
			} `json:"trae"`
		} `json:"_meta"`
	}
	var models []model
	if err := json.Unmarshal(body, &models); err != nil {
		var envelope struct {
			Models []model `json:"models"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return SubscriptionQuota{}, fmt.Errorf("decode traex models: %w", err)
		}
		models = envelope.Models
	}
	q := SubscriptionQuota{Windows: []QuotaWindow{}}
	for _, m := range models {
		if source.LoadModel != "" && m.Name == source.LoadModel {
			load := m.Meta.Trae.Load.Percent
			if load != nil && !math.IsNaN(*load) && !math.IsInf(*load, 0) && *load >= 0 {
				q.LoadPercent = load
			}
		}
		if source.WeeklyModel == "" || m.Name != source.WeeklyModel {
			continue
		}
		w := m.Meta.Trae.Weekly
		if w.Applies != nil && !*w.Applies {
			continue
		}
		if w.Used == nil || w.Reset == nil || math.IsNaN(*w.Used) || math.IsInf(*w.Used, 0) || *w.Used < 0 || *w.Used > 100 || math.IsNaN(*w.Reset) || math.IsInf(*w.Reset, 0) {
			continue
		}
		reset := time.Unix(int64(*w.Reset), 0)
		if !reset.After(now) {
			continue
		}
		q.Windows = append(q.Windows, QuotaWindow{Name: "7 days", Used: *w.Used, ResetsAt: &reset, Span: 7 * 24 * time.Hour})
	}
	return q, nil
}
