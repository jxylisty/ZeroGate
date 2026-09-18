// Weekly claim for ZCode weekend build: every Friday 23:30, call each bridged
// zcode-proxy instance's /admin/claim-now endpoint once (single attempt per
// account, no retry loop — retry loops are what got accounts WAF-flagged).
// See AGENTS.md §7.8/7.9 for the mechanism and the 3001-header pitfall.
package zcode

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"wild-work/internal/auth"
	"wild-work/internal/pool"
)

// StartWeeklyClaim 启动周五 23:30 的周末包领取 goroutine。
// bridgeKey 为访问实例管理端点的凭证（ZCODE_BRIDGE_KEY）。
func StartWeeklyClaim(ctx context.Context, p *pool.Pool, bridgeKey string) {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		var lastTrigger time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				if now.Weekday() != time.Friday || now.Hour() != 23 || now.Minute() < 30 || now.Minute() >= 45 {
					continue
				}
				if lastTrigger.IsZero() || lastTrigger.Year() != now.Year() || lastTrigger.YearDay() != now.YearDay() {
					lastTrigger = now
					runWeeklyClaim(p, bridgeKey)
				}
			}
		}
	}()
	log.Printf("zcode: 周末包领取调度已启动（周五 23:30 单次，账号级桥接）")
}

// runWeeklyClaim 对池中每个带 BridgeURL 的账号执行一次 claim。
func runWeeklyClaim(p *pool.Pool, bridgeKey string) {
	for _, a := range p.Auths() {
		if a == nil || strings.TrimSpace(a.BridgeURL) == "" {
			continue
		}
		result, err := claimViaInstance(a, bridgeKey)
		if err != nil {
			log.Printf("zcode weekly claim uid=%s bridge=%s error=%v", a.UID, a.BridgeURL, err)
			continue
		}
		sum := summarizeClaim(result)
		log.Printf("zcode weekly claim uid=%s bridge=%s ok=%v %s", a.UID, a.BridgeURL, result.OK, sum)
	}
}

type claimResult struct {
	OK      bool      `json:"ok"`
	PlanID  string    `json:"planId,omitempty"`
	Claimed *claimOut `json:"claimed,omitempty"`
	Plans   []any     `json:"plans,omitempty"`
	Error   string    `json:"error,omitempty"`
}

type claimOut struct {
	PlanID   string `json:"planId"`
	StartsAt int64  `json:"startsAt,omitempty"`
	EndsAt   int64  `json:"endsAt,omitempty"`
}

// claimViaInstance POST {bridgeUrl}/admin/claim-now（鉴权 Bearer bridgeKey）。
func claimViaInstance(a *auth.Auth, bridgeKey string) (*claimResult, error) {
	body, _ := json.Marshal(map[string]string{"mode": "now"})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(a.BridgeURL, "/")+"/admin/claim-now", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if bridgeKey != "" {
		req.Header.Set("Authorization", "Bearer "+bridgeKey)
	}
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r claimResult
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func summarizeClaim(r *claimResult) string {
	if r.Claimed != nil {
		return "claimed plan=" + r.Claimed.PlanID
	}
	if r.Error != "" {
		return "error=" + r.Error
	}
	if len(r.Plans) > 0 {
		return "plans_available=" + itoa(len(r.Plans)) + " (not claimed)"
	}
	return "no claimable plans"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// BridgeKeyFromEnv 读取全局桥接 key（供 main 装配用）。
func BridgeKeyFromEnv() string {
	return strings.TrimSpace(os.Getenv("ZCODE_BRIDGE_KEY"))
}