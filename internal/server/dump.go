package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 请求捕获诊断（默认关闭，仅排查上游拒绝类问题时临时开启）。
//
// 触发条件：环境变量 ZG_DUMP_BODY=1。
// 用途：上游返回 4xx（典型如 intl 11128 "Illegal API invocation from an
// unapproved channel"）时，把网关实际转发出去的请求体落盘，便于与成功请求
// 逐字段对比，定位是哪个字段/结构触发了上游的渠道校验。
//
// 安全：dump 内容不含 Authorization 等凭证；请求体中的 API Key 字段会被掩码。
// 落盘位置：$ZG_DUMP_DIR（默认 ./data/dumps），文件名含 UTC 时间戳与结果码。
var (
	dumpOnce sync.Once
	dumpOn   bool
	dumpDir  string
)

func dumpInit() {
	dumpOn = os.Getenv("ZG_DUMP_BODY") == "1"
	dumpDir = os.Getenv("ZG_DUMP_DIR")
	if dumpDir == "" {
		dumpDir = filepath.Join("data", "dumps")
	}
	if dumpOn {
		_ = os.MkdirAll(dumpDir, 0o755)
	}
}

// dumpRequestBody 把一次失败请求的上下文写入 dump 目录。
// 仅在 ZG_DUMP_BODY=1 时生效，任何错误都静默（诊断不影响主链路）。
func dumpRequestBody(channel, model string, status int, respBody []byte, reqBody []byte) {
	if !dumpOn {
		return
	}
	rec := map[string]any{
		"ts":            time.Now().Format(time.RFC3339Nano),
		"channel":       channel,
		"model":         model,
		"status":        status,
		"upstream_resp": maskSecrets(string(respBody)),
		"request_body":  redactJSON(reqBody),
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return
	}
	name := fmt.Sprintf("%s_%s_%d.json",
		time.Now().Format("20060102-150405.000"), sanitize(channel), status)
	_ = os.WriteFile(filepath.Join(dumpDir, name), raw, 0o600)
}

// redactJSON 将请求体解析后掩码敏感字段；解析失败则原样返回截断文本。
func redactJSON(raw []byte) any {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return truncateDump(string(raw))
	}
	redactValue(v)
	return v
}

func redactValue(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "key") || strings.Contains(lk, "token") ||
				strings.Contains(lk, "secret") || strings.Contains(lk, "password") ||
				strings.Contains(lk, "authorization") || strings.Contains(lk, "auth") {
				if s, ok := val.(string); ok && s != "" {
					t[k] = "***REDACTED***"
					continue
				}
			}
			redactValue(val)
		}
	case []any:
		for _, item := range t {
			redactValue(item)
		}
	}
}

// maskSecrets 掩码响应文本中可能出现的凭证串（Bearer/sk- 等）。
func maskSecrets(s string) string {
	if s == "" {
		return s
	}
	for _, pat := range []string{"sk-zg-", "sk-", "Bearer ", "atr_"} {
		idx := 0
		for {
			p := strings.Index(s[idx:], pat)
			if p < 0 {
				break
			}
			start := idx + p + len(pat)
			end := start
			for end < len(s) && !strings.ContainsRune(" \",}\n\r", rune(s[end])) {
				end++
			}
			s = s[:start] + "***" + s[end:]
			idx = start + 3
		}
	}
	return truncateDump(s)
}

func truncateDump(s string) string {
	const max = 200000
	if len(s) > max {
		return s[:max] + "\n...[truncated]"
	}
	return s
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

func ensureDumpInit() { dumpOnce.Do(dumpInit) }
