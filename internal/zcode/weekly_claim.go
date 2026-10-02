// Weekly claim for ZCode weekend build: every Friday/Saturday 19:00-19:15,
// run `zcode-proxy claim now` once per instance (CLI mode; the HTTP
// /admin/claim-now endpoint kills the instance process — see lessons/04).
// 2026-09-21: results are persisted (data/zcode-claim-result.json) and
// surfaced on the panel; manual trigger available via zcode.ClaimNow.
package zcode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"zerogate/internal/platform"
)

// ClaimRecord 单实例领取结果。
type ClaimRecord struct {
	Instance string `json:"instance"`
	Output   string `json:"output"`
	OK       bool   `json:"ok"`    // true = 领到
	Failed   bool   `json:"failed"`
	At       string `json:"at"` // 15:04:05
}

// ClaimReport 一次批量领取报告。
type ClaimReport struct {
	At        string        `json:"at"`
	Trigger   string        `json:"trigger"` // auto | manual
	Total     int           `json:"total"`
	Claimed   int           `json:"claimed"`
	NoClaim   int           `json:"no_claim"`
	Failed    int           `json:"failed"`
	Instances []ClaimRecord `json:"instances"`
}

// claimResultFile 领取结果落盘路径（main 启动时按 stateDir 重设）。
var (
	claimResultMu   sync.Mutex
	claimResultFile = "data/zcode-claim-result.json"
)

// zcodeClaimExe 领取 CLI。用独立文件名 zcode-proxy-claim.exe：常驻实例占用
// zcode-proxy.exe（Windows 锁 exe），无法就地覆盖，故改指向带载体页支持的新件。
const zcodeClaimExe = "D:/wild-work/research/zcode-proxy/zcode-proxy-claim.exe"

// captchaMintBudget 载体页 mint 的总预算（略大于 MintCaptchaParam 内部 90s 等待，
// 让更友好的超时文案先生效）。
const captchaMintBudget = 95 * time.Second

// carrierBase 载体页基地址（如 http://127.0.0.1:7863），由 main 按监听配置装配。
var (
	carrierBaseMu sync.Mutex
	carrierBase   string
)

// SetCarrierBaseURL 设置载体页基地址（须在首次领取前调用）。
func SetCarrierBaseURL(base string) {
	carrierBaseMu.Lock()
	carrierBase = strings.TrimRight(strings.TrimSpace(base), "/")
	carrierBaseMu.Unlock()
}

// CarrierBaseURL 返回载体页基地址（空表示未配置，领取时跳过 mint）。
func CarrierBaseURL() string {
	carrierBaseMu.Lock()
	defer carrierBaseMu.Unlock()
	return carrierBase
}

// SetClaimResultFile 重设领取结果文件路径（须在首次领取前调用）。
func SetClaimResultFile(fp string) {
	claimResultMu.Lock()
	claimResultFile = fp
	claimResultMu.Unlock()
}

// ClaimResultFile 领取结果文件路径（面板回读用）。
func ClaimResultFile() string {
	claimResultMu.Lock()
	defer claimResultMu.Unlock()
	return claimResultFile
}

// StartWeeklyClaim 启动周五/周六 19:00 的周末包领取 goroutine（卡片 19 点投放即领，23:00 激活；每日名额第二天可能刷新补货）。
// bridgeKey 为访问实例管理端点的凭证（ZCODE_BRIDGE_KEY）。
func StartWeeklyClaim(ctx context.Context, bridgeKey string) {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		var lastTrigger time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				// 周五+周六 19:00：周末包每日名额刷新（用户经验：当天领完第二天会补货）
				if (now.Weekday() != time.Friday && now.Weekday() != time.Saturday) || now.Hour() != 19 || now.Minute() >= 15 {
					continue
				}
				if lastTrigger.IsZero() || lastTrigger.Year() != now.Year() || lastTrigger.YearDay() != now.YearDay() {
					lastTrigger = now
					rep := ClaimNow("auto")
					log.Printf("zcode weekly claim(auto): claimed=%d no_claim=%d failed=%d", rep.Claimed, rep.NoClaim, rep.Failed)
				}
			}
		}
	}()
	log.Printf("zcode: 周末包领取调度已启动（周五/周六 19:00，账号级桥接）")
}

// ClaimNow 立即执行一轮全实例领取并落盘结果（定时与面板手动共用）。
func ClaimNow(trigger string) ClaimReport {
	rep := runWeeklyClaim(trigger)
	saveClaimReport(rep)
	return rep
}

// ClaimLastReport 最近一次领取报告（无记录返回 nil）。
func ClaimLastReport() *ClaimReport {
	raw, err := os.ReadFile(ClaimResultFile())
	if err != nil {
		return nil
	}
	var rep ClaimReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil
	}
	return &rep
}

func saveClaimReport(rep ClaimReport) {
	fp := ClaimResultFile()
	if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
		log.Printf("zcode weekly claim: 结果目录创建失败 %s: %v", fp, err)
		return
	}
	raw, _ := json.MarshalIndent(rep, "", "  ")
	tmp := fp + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		log.Printf("zcode weekly claim: 结果写盘失败 %s: %v", fp, err)
		return
	}
	_ = os.Rename(tmp, fp)
}

// classifyClaim 解析 CLI 输出：Claimed / No claimable / error。
func classifyClaim(out string) (ok, failed bool) {
	s := strings.ToLower(out)
	switch {
	case strings.Contains(s, "claimed") && !strings.Contains(s, "no claimable"):
		return true, false
	case strings.Contains(s, "error") || strings.Contains(s, "fail"):
		return false, true
	default:
		return false, false // 含 "no claimable" 及未知输出：未领到但非失败
	}
}

// runWeeklyClaim 对每个 zcode-proxy 实例执行一次 CLI claim（实例目录遍历）。
// 不用 HTTP /admin/claim-now：实测该端点处理后会弄死实例进程（2026-09-18）；
// CLI 方式（claim now）19:2x 验证可靠且幂等。
func runWeeklyClaim(trigger string) ClaimReport {
	instDir := os.Getenv("ZCODE_PROXY_INSTANCES_DIR")
	if strings.TrimSpace(instDir) == "" {
		instDir = "D:/wild-work/research/zcode-proxy/instances"
	}
	rep := ClaimReport{At: time.Now().Format("2006-01-02 15:04:05"), Trigger: trigger}
	// 仅手动「立即领取」走浏览器载体页：自动窗口无人在场，弹标签页只会白等
	// 90s/号再回退，不如直接用原 CLI 自解。
	useBrowser := trigger == "manual"
	dirs, err := os.ReadDir(instDir)
	if err != nil {
		log.Printf("zcode weekly claim: 实例目录不可读 %s: %v", instDir, err)
		rep.Instances = append(rep.Instances, ClaimRecord{
			Instance: "(instances)", Output: fmt.Sprintf("实例目录不可读: %v", err),
			Failed: true, At: time.Now().Format("15:04:05"),
		})
		rep.Failed = 1
		rep.Total = 1
		return rep
	}
	for _, d := range dirs {
		if !d.IsDir() || !strings.HasPrefix(d.Name(), "acc-") {
			continue
		}
		credsDir := filepath.Join(instDir, d.Name(), "creds")
		out, err := claimCLI(credsDir, useBrowser)
		rec := ClaimRecord{Instance: d.Name(), Output: strings.TrimSpace(out), At: time.Now().Format("15:04:05")}
		if err != nil {
			rec.Output = strings.TrimSpace(rec.Output + " " + err.Error())
			rec.Failed = true
			rep.Failed++
		} else {
			rec.OK, rec.Failed = classifyClaim(out)
			switch {
			case rec.OK:
				rep.Claimed++
			case rec.Failed:
				rep.Failed++
			default:
				rep.NoClaim++
			}
		}
		rep.Total++
		rep.Instances = append(rep.Instances, rec)
		log.Printf("zcode weekly claim %s: %s", d.Name(), rec.Output)
	}
	return rep
}

// claimCLI 以 CLI 单次模式执行 claim（输出含 Claimed/No claimable/error）。
func claimCLI(credsDir string, useBrowser bool) (string, error) {
	exe := zcodeClaimExe
	cmd := exec.Command(exe, "claim", "now")
	platform.HideWindow(cmd)
	env := append(os.Environ(),
		"ZCODE_CREDENTIALS_DIR="+credsDir,
		"ZCODE_CLAIM_ENABLED=false",
		"HTTPS_PROXY=", "HTTP_PROXY=", "ALL_PROXY=",
	)
	// 关键：把配置指向本实例自己的 config.yaml。CLI 默认读工作目录的
	// config.yaml，若全部实例共用根配置，则 X-Device-Mid/appVersion 也是同一个；
	// 活动网关按设备去重 —— 第一个号领到后，其余实例的 preview 会被判成
	// 「无可领计划」而静默漏领（2026-09-29 实证：4 个号只有一个领到）。
	// 实例目录没有 config.yaml 时退回原行为，避免 CLI 直接报 config not found。
	if cfg := filepath.Join(filepath.Dir(credsDir), "config.yaml"); fileExists(cfg) {
		env = append(env, "ZCODE_PROXY_CONFIG="+cfg)
	}
	// 载体页 mint：用用户真实浏览器产出 param 注入 CLI，绕开 happy-dom 假 DOM
	// 被风控判风险的问题。mint 失败（超时/未配置）则回退 CLI 自解，不阻塞领取。
	if base := CarrierBaseURL(); useBrowser && base != "" {
		mintCtx, cancel := context.WithTimeout(context.Background(), captchaMintBudget)
		cp, mintErr := MintCaptchaParam(mintCtx, base)
		cancel()
		if mintErr == nil {
			env = append(env, "ZCODE_CAPTCHA_PARAM="+cp.Param)
			if cp.Region != "" {
				env = append(env, "ZCODE_CAPTCHA_REGION="+cp.Region)
			}
		} else {
			log.Printf("zcode claim %s: 载体页 mint 失败，回退 CLI 自解：%v",
				filepath.Base(filepath.Dir(credsDir)), mintErr)
		}
	}
	cmd.Env = env
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

// fileExists 文件是否存在（claim 的实例配置探测）。
func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// BridgeKeyFromEnv 读取全局桥接 key（供 main 装配用）。
func BridgeKeyFromEnv() string {
	return strings.TrimSpace(os.Getenv("ZCODE_BRIDGE_KEY"))
}
