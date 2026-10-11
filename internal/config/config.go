// Package config 加载 JSON 配置 + 环境变量覆盖，并支持原子写回（GUI 面板修改）。
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Listen 监听地址：Host 为空表示全部接口。
type Listen struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// Addr 返回 net.Listen 使用的地址串，如 "127.0.0.1:7863" / ":7863"。
func (l Listen) Addr() string {
	host := l.Host
	port := l.Port
	if port <= 0 {
		port = 7863
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// UnmarshalJSON 兼容旧版字符串形式（":7863" / "127.0.0.1:9999" / "9999"）。
func (l *Listen) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		l2, err := ParseListen(s)
		if err != nil {
			return err
		}
		*l = l2
		return nil
	}
	var o struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err := json.Unmarshal(data, &o); err != nil {
		return err
	}
	l.Host, l.Port = o.Host, o.Port
	return nil
}

// ParseListen 解析 "host:port" / ":port" / "port" 三种形式。
func ParseListen(s string) (Listen, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Listen{}, fmt.Errorf("empty listen address")
	}
	host, portStr := s, ""
	if i := strings.LastIndex(s, ":"); i >= 0 {
		host, portStr = s[:i], s[i+1:]
	} else if n, err := strconv.Atoi(host); err == nil {
		// 无冒号且整体是数字 → 仅端口（兼容旧配置 "7863"）
		return Listen{Port: n}, nil
	}
	port := 0
	if portStr != "" {
		n, err := strconv.Atoi(portStr)
		if err != nil || n <= 0 || n > 65535 {
			return Listen{}, fmt.Errorf("bad listen port %q", portStr)
		}
		port = n
	}
	return Listen{Host: host, Port: port}, nil
}

// Config 顶层配置。
type Config struct {
	Listen Listen `json:"listen"`
	// ListenDashboard 只读 Token 大屏专用监听（可选）。Port>0 时额外起一个只读监听，
	// 仅暴露 Token 大屏所需匿名读端点 + 静态 UI，不暴露管理 API（/api/state 含 API-Key）。
	// 供手机 / 局域网访问大屏使用；主监听（Listen）仍保持仅回环。
	ListenDashboard *Listen `json:"listen_dashboard,omitempty"`
	APIKey          string  `json:"api_key"` // 空 = 不鉴权
	AuthDir   string `json:"auth_dir"`   // ./auths
	StateFile string `json:"state_file"` // ./data/state.json
	Region    string `json:"region"`     // 只收 "cn"

	Cooldown struct {
		HardCredit  string `json:"hard_credit"`   // "12h"
		SoftRate    string `json:"soft_rate"`     // "60s"（429 软限流）
		Net         string `json:"net"`           // "10s"（网络类错误：DNS/dial/读写/桥不可达等非账号问题）
		ErrThresh   int    `json:"err_threshold"` // 默认 3
		ErrCooldown string `json:"err_cooldown"`  // "10m"
	} `json:"cooldown"`

	Schedule struct {
		CheckinHours   []int    `json:"checkin_hours,omitempty"` // 旧格式：[9,21]
		CheckinTimes   []string `json:"checkin_times,omitempty"` // 新格式："09:00","21:30"]
		GrowthTimes    []string `json:"growth_times,omitempty"`  // 成长中心执行时间："09:00","21:30"]
		KeepaliveHours []int    `json:"keepalive_hours"`         // [22]
	} `json:"schedule"`

	Upstream struct {
		TimeoutSeconds int `json:"timeout_seconds"` // 默认 120
	} `json:"upstream"`

	RateLimit struct {
		RequestsPerHour int `json:"requests_per_hour"` // 每号每小时请求上限，默认 300（GLM_proxy 同款）
	} `json:"rate_limit"`

	ModelRedirects map[string][]string `json:"model_redirects,omitempty"` // 模型别名映射与容灾列表
	ModelMerge     map[string][]string `json:"model_merge,omitempty"`     // 合并路由：跨渠道选到期最近/最低余额账号

	// 解析后
	HardCreditDur  time.Duration `json:"-"`
	SoftRateDur    time.Duration `json:"-"`
	NetDur         time.Duration `json:"-"`
	ErrCooldownDur time.Duration `json:"-"`
}

// DefaultModelRedirects 返回开箱即用的默认模型别名与备选链。
func DefaultModelRedirects() map[string][]string {
	return map[string][]string{
		// claude 系别名已移除：wbintl 渠道下线、workbuddy 上游 11102 不再提供这些模型（2026-10 核实全渠道无可用 claude）
		"deepseek-chat":                 {"codebuddy/deepseek-v4.1-flash", "qoder/glm-5.3-flash", "traework/glm-5.2"},
		"deepseek-v3":                   {"codebuddy/deepseek-v4.1-flash", "qoder/glm-5.3-flash", "traework/glm-5.2"},
		"deepseek-reasoner":             {"codebuddy/deepseek-v4.1-flash", "qoder/glm-5.3-flash", "traework/glm-5.2"},
		"deepseek-r1":                   {"codebuddy/deepseek-v4.1-flash", "qoder/glm-5.3-flash", "traework/glm-5.2"},
		"workbuddy/deepseek-v4.1-flash": {"codebuddy/deepseek-v4.1-flash", "qoder/glm-5.3-flash"}, // workbuddy 上游 11128 不可用 -> 走 codebuddy 同款(实测 200)
		// workbuddy/glm-5.3-flash 首选原渠道（upstream.PrepareBody 已剥离 gitStatus），
		// 仍被 11128 拦截则退到 zcode 同名模型（实测 system 带 Git user 段也稳定 200）。
		"workbuddy/glm-5.3-flash": {"workbuddy/glm-5.3-flash", "zcode/glm-5.3-flash"},
		"gpt-4o":                  {"qoder/gpt-4o", "traework/glm-5.2"},
		"qwen-2.5-coder":          {"traework/qwen3-coder", "workbuddy/glm-5.2"},
	}
}

// DefaultModelMerge 跨渠道合并路由：同款模型合并为一个虚拟名，自动优选到期最近的账号。
func DefaultModelMerge() map[string][]string {
	return map[string][]string{
		// ── DeepSeek 系 ──
		"deepseek-v4-flash": {"traework/DeepSeek-V4-Flash-Official", "traework/DeepSeek-V4-Flash", "traework/deepseek-v4.1-flash", "codebuddy/deepseek-v4.1-flash", "qoder/deepseek-flash"},
		"deepseek-v4-pro":   {"qoder/deepseek-v4-pro", "traework/DeepSeek-V4-Pro-Official", "traework/DeepSeek-V4-Pro", "workbuddy/deepseek-v4-pro"},
		// ── GLM 系 ──
		"glm-5.3":           {"traework/glm-5.3", "qoder/glm-5.3", "codebuddy/glm-5.3", "workbuddy/glm-5.3", "glmflash/glm-5.3", "zcode/glm-5.3"},
		"glm-5.3-flash":     {"qoder/glm-5.3-flash", "workbuddy/glm-5.3-flash", "glmflash/glm-5.3-flash", "zcode/glm-5.3-flash"},
		// ── Doubao 系 ──
		"doubao-seed-pro":   {"traework/Doubao-Seed-2.1-Pro"},
		"doubao-seed-turbo": {"traework/Doubao-Seed-2.1-Turbo"},
		"doubao-seed-code":  {"traework/Doubao-Seed-2.0-Code", "traework/seed-code-pro-0430"},
		// ── Kimi 系 ──
		"kimi-k3":           {"traework/kimi-k3", "qoder/kimi-k3", "codebuddy/kimi-k3", "workbuddy/kimi-k3-1"},
		"kimi-k2.7":         {"traework/kimi-k2.7-code", "workbuddy/kimi-k2.7", "workbuddy/kimi-k2.8-preview", "qoder/kimi-k2.8-preview"},
		// ── Qwen 系 ──
		"qwen3.8-max":       {"traework/qwen3.8-max", "qoder/qwen3.8-max"},
		"qwen3.8-flash":     {"qoder/qwen3.8-flash"},
		// ── GPT 系（本机白嫖 + codebuddy）──
		"gpt-5.6":           {"gptgemini/gpt-5.6-terra", "gptgemini/gpt-5.6-luna", "codebuddy/gpt-5.6-sol", "codebuddy/gpt-5.6-terra", "codebuddy/gpt-5.6-luna"},
		"gpt-5.5":           {"gptgemini/gpt-5.5", "codebuddy/gpt-5.5"},
		// ── Gemini 系 ──
		"gemini-3.1-pro":    {"gptgemini/gemini-3.1-pro"},
		"gemini-3-flash":    {"gptgemini/gemini-3-flash", "gptgemini/gemini-3.1-flash-lite", "codebuddy/gemini-3.5-flash"},
		// ── Grok 系 ──
		"grok-4.7":          {"gptgemini/grok-4.7", "gptgemini/grok-4.6"},
		// ── 其他 ──
		"minimax-m3":        {"traework/minimax-m3", "workbuddy/minimax-m3"},
	}
}

// Default 默认配置。
func Default() *Config {
	c := &Config{
		Listen:         Listen{Host: "127.0.0.1", Port: 7863},
		APIKey:         "WildWorkAPI",
		AuthDir:        "./auths",
		StateFile:      "./data/state.json",
		Region:         "cn",
		ModelRedirects: DefaultModelRedirects(),
		ModelMerge:     DefaultModelMerge(),
	}
	c.Cooldown.HardCredit = "12h"
	c.Cooldown.SoftRate = "60s"
	c.Cooldown.Net = "10s"
	c.Cooldown.ErrThresh = 3
	c.Cooldown.ErrCooldown = "10m"
	c.Schedule.CheckinHours = []int{9, 21}
	c.Schedule.CheckinTimes = []string{"09:00", "21:00"}
	c.Schedule.GrowthTimes = []string{"09:00", "21:00"}
	c.Schedule.KeepaliveHours = []int{22}
	c.Upstream.TimeoutSeconds = 120
	c.RateLimit.RequestsPerHour = 300
	return c
}

// Load 从文件读，再用 WILDWORK_* env 覆盖。
func Load(path string) (*Config, error) {
	c := Default()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
		if err := json.Unmarshal(raw, c); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
		// Default() 带有新字段默认值；老配置没有 checkin_times 时必须让旧的
		// checkin_hours 生效，而不能被 Default 的 [09:00,21:00] 覆盖。
		var shape struct {
			Schedule map[string]json.RawMessage `json:"schedule"`
		}
		if json.Unmarshal(raw, &shape) == nil {
			if _, ok := shape.Schedule["checkin_times"]; !ok {
				c.Schedule.CheckinTimes = nil
			}
		}
	}
	if len(c.ModelRedirects) == 0 {
		c.ModelRedirects = DefaultModelRedirects()
	}
	if len(c.ModelMerge) == 0 {
		c.ModelMerge = DefaultModelMerge()
	}
	applyEnv(c)
	if err := c.normalize(); err != nil {
		return nil, err
	}
	return c, nil
}

// Save 以 0600 原子写回配置。
func Save(c *Config, path string) error {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	// 覆盖前留一份 .bak：配置里的端口 / API key / 模型重定向（ModelRedirects）写错
	// 会直接让网关不可用，有备份才能一键回滚，避免"改错就完蛋"。
	if old, rerr := os.ReadFile(path); rerr == nil {
		_ = os.WriteFile(path+".bak", old, 0o600)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ParseClockTimes 将 HH:MM 列表转换为当天分钟数（0..1439）。
func ParseClockTimes(values []string) ([]int, error) {
	seen := map[int]bool{}
	out := make([]int, 0, len(values))
	for _, v := range values {
		parts := strings.Split(strings.TrimSpace(v), ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid time %q", v)
		}
		h, errH := strconv.Atoi(parts[0])
		m, errM := strconv.Atoi(parts[1])
		if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			return nil, fmt.Errorf("invalid time %q", v)
		}
		minute := h*60 + m
		if !seen[minute] {
			seen[minute] = true
			out = append(out, minute)
		}
	}
	sort.Ints(out)
	return out, nil
}

// FormatClockTimes 将当天分钟数格式化为排序后的 HH:MM 列表。
func FormatClockTimes(minutes []int) []string {
	out := make([]int, 0, len(minutes))
	seen := map[int]bool{}
	for _, m := range minutes {
		if m >= 0 && m < 24*60 && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Ints(out)
	formatted := make([]string, 0, len(out))
	for _, m := range out {
		formatted = append(formatted, fmt.Sprintf("%02d:%02d", m/60, m%60))
	}
	return formatted
}

func applyEnv(c *Config) {
	if v := os.Getenv("WILDWORK_LISTEN"); v != "" {
		if l, err := ParseListen(v); err == nil {
			c.Listen = l
		}
	}
	if v := os.Getenv("WILDWORK_API_KEY"); v != "" {
		c.APIKey = v
	}
	if v := os.Getenv("WILDWORK_AUTH_DIR"); v != "" {
		c.AuthDir = v
	}
	if v := os.Getenv("WILDWORK_STATE_FILE"); v != "" {
		c.StateFile = v
	}
	if v := os.Getenv("WILDWORK_REGION"); v != "" {
		c.Region = v
	}
	if v := os.Getenv("WILDWORK_HARD_CREDIT"); v != "" {
		c.Cooldown.HardCredit = v
	}
	if v := os.Getenv("WILDWORK_SOFT_RATE"); v != "" {
		c.Cooldown.SoftRate = v
	}
	if v := os.Getenv("WILDWORK_NET_COOLDOWN"); v != "" {
		c.Cooldown.Net = v
	}
	if v := os.Getenv("WILDWORK_ERR_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Cooldown.ErrThresh = n
		}
	}
	if v := os.Getenv("WILDWORK_ERR_COOLDOWN"); v != "" {
		c.Cooldown.ErrCooldown = v
	}
	if v := os.Getenv("WILDWORK_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Upstream.TimeoutSeconds = n
		}
	}
	if v := os.Getenv("WILDWORK_GROWTH_TIMES"); v != "" {
		c.Schedule.GrowthTimes = parseTimeList(v)
	}
}

func (c *Config) normalize() error {
	var err error
	if c.HardCreditDur, err = time.ParseDuration(c.Cooldown.HardCredit); err != nil {
		return fmt.Errorf("cooldown.hard_credit: %w", err)
	}
	if c.SoftRateDur, err = time.ParseDuration(c.Cooldown.SoftRate); err != nil {
		return fmt.Errorf("cooldown.soft_rate: %w", err)
	}
	if c.NetDur, err = time.ParseDuration(c.Cooldown.Net); err != nil {
		return fmt.Errorf("cooldown.net: %w", err)
	}
	if c.ErrCooldownDur, err = time.ParseDuration(c.Cooldown.ErrCooldown); err != nil {
		return fmt.Errorf("cooldown.err_cooldown: %w", err)
	}
	if c.Cooldown.ErrThresh <= 0 {
		c.Cooldown.ErrThresh = 3
	}
	if c.Upstream.TimeoutSeconds <= 0 {
		c.Upstream.TimeoutSeconds = 120
	}
	if c.RateLimit.RequestsPerHour <= 0 {
		c.RateLimit.RequestsPerHour = 300
	}
	if c.Listen.Port <= 0 {
		c.Listen.Port = 7863
	}
	// listen_dashboard：仅当显式配置了 port>0 才启用只读大屏监听；
	// port<=0 视为未启用，置 nil，避免误开一个空的监听端口。
	if c.ListenDashboard != nil {
		if c.ListenDashboard.Port <= 0 {
			c.ListenDashboard = nil
		} else if c.ListenDashboard.Host == "" {
			c.ListenDashboard.Host = "0.0.0.0"
		}
	}
	if c.Region == "" {
		c.Region = "cn"
	}
	c.Region = strings.ToLower(c.Region)
	if c.Region != "cn" && c.Region != "global" {
		return fmt.Errorf("region must be cn or global, got %q", c.Region)
	}
	// 兼容旧版 checkin_hours；新版本统一规范化为 HH:MM。
	if len(c.Schedule.CheckinTimes) == 0 {
		c.Schedule.CheckinTimes = make([]string, 0, len(c.Schedule.CheckinHours))
		for _, h := range c.Schedule.CheckinHours {
			if h < 0 || h > 23 {
				return fmt.Errorf("schedule.checkin_hours: hour out of range: %d", h)
			}
			c.Schedule.CheckinTimes = append(c.Schedule.CheckinTimes, fmt.Sprintf("%02d:00", h))
		}
	}
	mins, err := ParseClockTimes(c.Schedule.CheckinTimes)
	if err != nil {
		return fmt.Errorf("schedule.checkin_times: %w", err)
	}
	c.Schedule.CheckinTimes = FormatClockTimes(mins)
	if len(c.Schedule.CheckinHours) == 0 {
		for _, m := range mins {
			if m%60 == 0 {
				c.Schedule.CheckinHours = append(c.Schedule.CheckinHours, m/60)
			}
		}
	}
	// growth_times 规范化
	if len(c.Schedule.GrowthTimes) > 0 {
		mins, err := ParseClockTimes(c.Schedule.GrowthTimes)
		if err != nil {
			return fmt.Errorf("schedule.growth_times: %w", err)
		}
		c.Schedule.GrowthTimes = FormatClockTimes(mins)
	}
	return nil
}

// parseTimeList 解析逗号分隔的时间列表 "09:00,21:30"
func parseTimeList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
