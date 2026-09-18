// Weekly claim for ZCode weekend build: every Friday 23:30, call each bridged
// zcode-proxy instance's /admin/claim-now endpoint once (single attempt per
// account, no retry loop — retry loops are what got accounts WAF-flagged).
// See AGENTS.md §7.8/7.9 for the mechanism and the 3001-header pitfall.
package zcode

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"wild-work/internal/pool"
)

// StartWeeklyClaim 启动周五 19:00 的周末包领取 goroutine（卡片 19 点投放即领，23:00 激活）。
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
				if now.Weekday() != time.Friday || now.Hour() != 19 || now.Minute() < 0 || now.Minute() >= 15 {
					continue
				}
				if lastTrigger.IsZero() || lastTrigger.Year() != now.Year() || lastTrigger.YearDay() != now.YearDay() {
					lastTrigger = now
					runWeeklyClaim(p, bridgeKey)
				}
			}
		}
	}()
	log.Printf("zcode: 周末包领取调度已启动（周五 19:00 单次，账号级桥接）")
}

// runWeeklyClaim 对每个 zcode-proxy 实例执行一次 CLI claim（实例目录遍历）。
// 不用 HTTP /admin/claim-now：实测该端点处理后会弄死实例进程（2026-09-18）；
// CLI 方式（claim now）19:2x 验证可靠且幂等。
func runWeeklyClaim(p *pool.Pool, bridgeKey string) {
	instDir := strings.TrimSpace(os.Getenv("ZCODE_PROXY_INSTANCES_DIR"))
	if instDir == "" {
		instDir = "D:/wild-work/research/zcode-proxy/instances"
	}
	dirs, err := os.ReadDir(instDir)
	if err != nil {
		log.Printf("zcode weekly claim: 实例目录不可读 %s: %v", instDir, err)
		return
	}
	for _, d := range dirs {
		if !d.IsDir() || !strings.HasPrefix(d.Name(), "acc-") {
			continue
		}
		credsDir := filepath.Join(instDir, d.Name(), "creds")
		out, err := claimCLI(credsDir)
		if err != nil {
			log.Printf("zcode weekly claim %s: %v", d.Name(), err)
			continue
		}
		log.Printf("zcode weekly claim %s: %s", d.Name(), strings.TrimSpace(out))
	}
}

// claimCLI 以 CLI 单次模式执行 claim（输出含 Claimed/No claimable/error）。
func claimCLI(credsDir string) (string, error) {
	exe := "D:/wild-work/research/zcode-proxy/zcode-proxy.exe"
	cmd := exec.Command(exe, "claim", "now")
	cmd.Env = append(os.Environ(),
		"ZCODE_CREDENTIALS_DIR="+credsDir,
		"ZCODE_CLAIM_ENABLED=false",
		"HTTPS_PROXY=", "HTTP_PROXY=", "ALL_PROXY=",
	)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.Dir = "D:/wild-work/research/zcode-proxy"
	// 单次执行，总超时 90s（captcha 求解在内）
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil && buf.Len() == 0 {
			return "", err
		}
		return buf.String(), nil
	case <-time.After(90 * time.Second):
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("claim 超时（90s）")
	}
}

// BridgeKeyFromEnv 读取全局桥接 key（供 main 装配用）。
func BridgeKeyFromEnv() string {
	return strings.TrimSpace(os.Getenv("ZCODE_BRIDGE_KEY"))
}