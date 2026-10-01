package provider

import (
	"testing"
	"time"
)

func TestTraexUsesConfiguredModelsWithoutBorrowingLoad(t *testing.T) {
	body := []byte(`[{"name":"weekly","_meta":{"trae":{"weeklyQuota":{"applies":true,"usedPercent":7,"remainingPercent":93,"resetTime":1791129599},"load":{"percent":189}}}},{"name":"load","_meta":{"trae":{"load":{"percent":32}}}}]`)
	now := time.Unix(1791000000, 0)
	q, err := parseTraexQuota(body, UsageSource{WeeklyModel: "weekly", LoadModel: "load"}, now)
	if err != nil || len(q.Windows) != 1 || q.Windows[0].Used != 7 || q.Windows[0].Span != 7*24*time.Hour || q.LoadPercent == nil || *q.LoadPercent != 32 {
		t.Fatalf("quota %+v: %v", q, err)
	}
	q, err = parseTraexQuota(body, UsageSource{WeeklyModel: "weekly", LoadModel: "missing"}, now)
	if err != nil || len(q.Windows) != 1 || q.LoadPercent != nil {
		t.Fatalf("borrowed load %+v: %v", q, err)
	}
	q, err = parseTraexQuota(body, UsageSource{WeeklyModel: "missing", LoadModel: "weekly"}, now)
	if err != nil || len(q.Windows) != 0 || q.LoadPercent == nil || *q.LoadPercent != 189 {
		t.Fatalf("load over 100 %+v: %v", q, err)
	}
}

func TestTraexUnusableWeeklyQuotaDoesNotBecomeZero(t *testing.T) {
	for _, quota := range []string{
		`{"applies":false,"usedPercent":0,"resetTime":1791129599}`,
		`{"applies":true,"resetTime":1791129599}`,
		`{"applies":true,"usedPercent":101,"resetTime":1791129599}`,
		`{"applies":true,"usedPercent":0,"resetTime":1790000000}`,
	} {
		q, err := parseTraexQuota([]byte(`{"models":[{"name":"weekly","_meta":{"trae":{"weeklyQuota":`+quota+`}}}]}`), UsageSource{WeeklyModel: "weekly"}, time.Unix(1791000000, 0))
		if err != nil || len(q.Windows) != 0 {
			t.Fatalf("unusable weekly quota %+v: %v", q, err)
		}
	}
}
