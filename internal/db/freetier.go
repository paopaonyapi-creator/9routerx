// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package db

import (
	"context"
	"fmt"
	"log"
	"time"
)

// freetier.go backs FASE 10.1: live tracking of what the well-known
// free-tier providers have actually served this period. The freeTierBudget
// table holds one row per provider; the usage writer bumps it after the
// ledger insert, and the reset loop below rolls usedThisPeriod over on each
// provider's own reset day.
//
// The quota figures are the same estimates the dashboard's free-tier catalog
// page cites (web/src/lib/freeTierCatalog.ts), keyed by the PROVIDER ID the
// gateway routes with — usageHistory.provider — not by the display name the
// page prints.

// FreeTierBudget is one provider's live free-tier counter.
type FreeTierBudget struct {
	Provider       string `json:"provider"`
	MonthlyQuota   int64  `json:"monthlyQuota"`
	ResetDay       int    `json:"resetDay"`
	UsedThisPeriod int64  `json:"usedThisPeriod"`
	LastUpdated    string `json:"lastUpdated"`
}

// freeTierCatalogSeed mirrors the dashboard's curated free-tier catalog: the
// gateway provider id each tier routes through, its steady-monthly token
// estimate, and the day of month the tier rolls over. resetDay 0 marks a
// one-off credit that never resets. Figures and their evidence live in the
// frontend catalog; this seed only needs the numbers the live tracking
// enforces, so it carries no notes.
//
// The seed is applied with INSERT OR IGNORE on every boot: it creates the
// rows tracking needs to exist, and never clobbers an operator override
// written through SetFreeTierBudget.
var freeTierCatalogSeed = []FreeTierBudget{
	{Provider: "gemini", MonthlyQuota: 30_000_000, ResetDay: 1},       // Google AI Studio: 250 req/day
	{Provider: "groq", MonthlyQuota: 120_000_000, ResetDay: 1},        // Groq: ~1,000 req/day on 70B
	{Provider: "cerebras", MonthlyQuota: 30_000_000, ResetDay: 1},     // Cerebras: 1M tokens/day
	{Provider: "mistral", MonthlyQuota: 1_000_000_000, ResetDay: 1},   // Mistral Experiment: 1B tokens/month
	{Provider: "openrouter", MonthlyQuota: 6_000_000, ResetDay: 1},    // OpenRouter :free: 50 req/day
	{Provider: "github", MonthlyQuota: 18_000_000, ResetDay: 1},       // GitHub Models: ~150 req/day
	{Provider: "cloudflare-ai", MonthlyQuota: 2_400_000, ResetDay: 1}, // Workers AI: 10k neurons/day
	{Provider: "nvidia", MonthlyQuota: 4_000_000, ResetDay: 0},        // NVIDIA NIM: 1,000 one-off credits
	{Provider: "huggingface", MonthlyQuota: 500_000, ResetDay: 0},     // HF inference: one-off credit
	{Provider: "together", MonthlyQuota: 2_000_000, ResetDay: 0},      // Together free lineup credit
	{Provider: "glm", MonthlyQuota: 50_000_000, ResetDay: 1},          // Z.AI GLM-4-Flash, concurrency-limited
}

// SeedFreeTierCatalogBudgets creates the catalog rows the tracking needs,
// leaving rows an operator already wrote alone. Called from OnStart next to
// the reset loop: an install that never opens the dashboard still books usage.
func (r *Repo) SeedFreeTierCatalogBudgets() error {
	for _, b := range freeTierCatalogSeed {
		if _, err := r.db.Exec(
			`INSERT OR IGNORE INTO freeTierBudget (provider, monthlyQuota, resetDay, usedThisPeriod, lastUpdated)
			 VALUES (?, ?, ?, 0, '')`,
			b.Provider, b.MonthlyQuota, b.ResetDay,
		); err != nil {
			return fmt.Errorf("seed free tier budget %s: %w", b.Provider, err)
		}
	}
	return nil
}

// SetFreeTierBudget upserts one provider's budget: the ceiling, and the day
// of month the counter rolls over (0 = never). An unknown provider creates
// its row, which is how an operator tracks a tier outside the seed.
func (r *Repo) SetFreeTierBudget(provider string, quota int64, resetDay int) error {
	if provider == "" {
		return fmt.Errorf("set free tier budget: empty provider")
	}
	_, err := r.db.Exec(
		`INSERT INTO freeTierBudget (provider, monthlyQuota, resetDay, usedThisPeriod, lastUpdated)
		 VALUES (?, ?, ?, 0, '')
		 ON CONFLICT(provider) DO UPDATE SET monthlyQuota = excluded.monthlyQuota, resetDay = excluded.resetDay`,
		provider, quota, resetDay,
	)
	if err != nil {
		return fmt.Errorf("set free tier budget %s: %w", provider, err)
	}
	return nil
}

// GetFreeTierBudgets returns every tracked provider's live counter.
func (r *Repo) GetFreeTierBudgets() ([]FreeTierBudget, error) {
	rows, err := r.db.Query(
		`SELECT provider, monthlyQuota, resetDay, usedThisPeriod, lastUpdated FROM freeTierBudget ORDER BY provider`,
	)
	if err != nil {
		return nil, fmt.Errorf("get free tier budgets: %w", err)
	}
	defer rows.Close()
	out := make([]FreeTierBudget, 0, 16)
	for rows.Next() {
		var b FreeTierBudget
		if err := rows.Scan(&b.Provider, &b.MonthlyQuota, &b.ResetDay, &b.UsedThisPeriod, &b.LastUpdated); err != nil {
			return nil, fmt.Errorf("get free tier budgets: scan: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get free tier budgets: %w", err)
	}
	return out, nil
}

// IncrementFreeTierUsage books one request's total tokens against its
// provider. A provider with no row is a no-op — only the tiers the operator
// or the seed tracks are counted — so the statement is safe to run after
// every ledger insert.
func (r *Repo) IncrementFreeTierUsage(provider string, tokens int64) error {
	if provider == "" || tokens <= 0 {
		return nil
	}
	_, err := r.db.Exec(
		`UPDATE freeTierBudget
		    SET usedThisPeriod = usedThisPeriod + ?, lastUpdated = ?
		  WHERE provider = ?`,
		tokens, time.Now().UTC().Format(time.RFC3339), provider,
	)
	if err != nil {
		return fmt.Errorf("increment free tier usage %s: %w", provider, err)
	}
	return nil
}

// ResetFreeTierPeriod zeroes one provider's counter and stamps the new period.
func (r *Repo) ResetFreeTierPeriod(provider string) error {
	_, err := r.db.Exec(
		`UPDATE freeTierBudget SET usedThisPeriod = 0, lastUpdated = ? WHERE provider = ?`,
		time.Now().UTC().Format(time.RFC3339), provider,
	)
	if err != nil {
		return fmt.Errorf("reset free tier period %s: %w", provider, err)
	}
	return nil
}

// ResetFreeTierPeriodsDue rolls every provider whose period has rolled over.
//
// A provider is due when its lastUpdated stamp predates the start of its
// CURRENT period — the most recent resetDay on or before now. Checking the
// period rather than "resetDay == today's date" is what makes the pass safe
// to run at any cadence: the hourly loop and a boot on the 5th for a tier
// with resetDay 3 both catch up, and running twice in one period resets
// nothing the second time. A resetDay beyond the month's length rolls on the
// month's last day (resetDay 31 fires on April 30).
//
// Returns the providers that were reset. now is injected so the boundary is
// testable without waiting a month.
func (r *Repo) ResetFreeTierPeriodsDue(now time.Time) ([]string, error) {
	budgets, err := r.GetFreeTierBudgets()
	if err != nil {
		return nil, err
	}
	now = now.UTC()
	var reset []string
	for _, b := range budgets {
		if b.ResetDay <= 0 {
			// One-off credit: never rolls.
			continue
		}
		periodStart := freeTierPeriodStart(now, b.ResetDay)
		if b.LastUpdated != "" {
			// A stamp we cannot parse reads as "no stamp": the counter is
			// reset rather than trusted, the same direction a billing counter
			// should fail.
			if stamped, perr := time.Parse(time.RFC3339, b.LastUpdated); perr == nil && !stamped.Before(periodStart) {
				// The stamp belongs to the current period: nothing due.
				continue
			}
		}
		if err := r.resetFreeTierPeriodAt(b.Provider, now); err != nil {
			return reset, err
		}
		reset = append(reset, b.Provider)
	}
	return reset, nil
}

// resetFreeTierPeriodAt zeroes one provider's counter and stamps the new
// period with the given instant — the pass's injected clock, not wall time,
// so a backdated or test-driven pass leaves a stamp that agrees with the
// period it opened.
func (r *Repo) resetFreeTierPeriodAt(provider string, now time.Time) error {
	_, err := r.db.Exec(
		`UPDATE freeTierBudget SET usedThisPeriod = 0, lastUpdated = ? WHERE provider = ?`,
		now.UTC().Format(time.RFC3339), provider,
	)
	if err != nil {
		return fmt.Errorf("reset free tier period %s: %w", provider, err)
	}
	return nil
}

// freeTierPeriodStart returns the most recent resetDay-of-month boundary at
// or before now. resetDay beyond a month's length lands on that month's last
// day (resetDay 31 fires on April 30, and February folds it into the 28th/29th),
// so a month-end tier never skips a short month. The clamping is explicit:
// time.Date would normalize an overflowing day into the NEXT month, which
// silently shifts the boundary forward a whole month.
func freeTierPeriodStart(now time.Time, resetDay int) time.Time {
	year, month, _ := now.Date()
	if candidate := monthBoundary(year, month, resetDay); !candidate.After(now) {
		return candidate
	}
	// The boundary falls later this month: the current period opened on the
	// previous month's reset day.
	prevYear, prevMonth := year, month-1
	if prevMonth < time.January {
		prevMonth = time.December
		prevYear--
	}
	return monthBoundary(prevYear, prevMonth, resetDay)
}

// monthBoundary is the resetDay of one month, clamped to that month's length.
func monthBoundary(year int, month time.Month, resetDay int) time.Time {
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	day := resetDay
	if day > lastDay {
		day = lastDay
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// DefaultFreeTierResetInterval is how often the running loop looks for due
// periods. An hour bounds the staleness of a displayed counter to one tick
// past the boundary while costing one tiny indexed read per hour.
const DefaultFreeTierResetInterval = time.Hour

// StartFreeTierResetLoop runs ResetFreeTierPeriodsDue on a timer until the
// context ends, and catches up once immediately so a gateway booted after a
// month boundary does not keep charging the old period. A pass that fails is
// logged and dropped: the next tick retried it.
func StartFreeTierResetLoop(ctx context.Context, repo *Repo) {
	if repo == nil {
		return
	}
	go func() {
		if _, err := repo.ResetFreeTierPeriodsDue(time.Now()); err != nil {
			log.Printf("[freetier] initial reset pass failed: %v", err)
		}
		ticker := time.NewTicker(DefaultFreeTierResetInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reset, err := repo.ResetFreeTierPeriodsDue(time.Now())
				if err != nil {
					log.Printf("[freetier] reset pass failed: %v", err)
					continue
				}
				if len(reset) > 0 {
					log.Printf("[freetier] rolled over %d free-tier period(s): %v", len(reset), reset)
				}
			}
		}
	}()
}
