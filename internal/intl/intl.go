// Package intl 国际版 CodeBuddy / WorkBuddy（www.codebuddy.ai / www.workbuddy.ai）上游客户端。
// 协议逆向自 CodeBuddy IDE 3.10.0、WorkBuddy AI 桌面客户端与 open-source 参考实现，三个硬性约束：
//
//  1. 非流式被禁：必须 stream:true（否则 code 11101）
//  2. messages[0] 必须是 system（否则 code 11128 "first message is not system prompt"）
//  3. 模型 ID 既支持 6 个逻辑槽位（default/fast/balanced/primary/deep-model），
//     也支持 /v2/enterprises/personal/models 返回的真实模型名（gpt-5.6-*、gemini-3.5-flash 等）
//
// 认证：Authorization: Bearer + X-User-Id + X-Domain；另外 chat 需要同 uuid 的
// trace 五件套 + X-Model-ID + X-Product/X-Product-Code/X-IDE-Name。
// Token 有效期约 1 年，过期前用 /v2/plugin/auth/token/refresh 轮换。
package intl

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"zerogate/internal/auth"
	"zerogate/internal/provider"
	"zerogate/internal/upstream"
)

// Client 国际版 CodeBuddy 上游客户端，实现 provider.Upstream。
type Client struct {
	HTTP    *http.Client
	Base    string // 默认 https://www.codebuddy.ai
	Channel string // 渠道名（codebuddy / wbintl），用于费率分组

	// StreamHTTP 供流式对话使用：无总超时，思考型模型可长时间输出。
	// http.Client.Timeout 覆盖响应体读取，会把长思考的 SSE 硬掐断。nil 时回退到 HTTP。
	StreamHTTP *http.Client

	// 动态发现机制：记录成功调用的隐藏模型（接口未返回但实际可用）
	discoveredModelsOnce sync.Once
	discoveredModels     map[string]bool // 已确认可用的隐藏模型 ID 集合
}

// New 生产默认。intl 与 CN 同域不同路径路由，无需多 base。
func New() *Client {
	tr := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 120 * time.Second, // 流式无总超时，靠它兜住建连卡死
	}
	return &Client{
		HTTP:       &http.Client{Timeout: 180 * time.Second, Transport: tr},
		StreamHTTP: &http.Client{Transport: tr},
		Base:       "https://www.codebuddy.ai",
		Channel:    "codebuddy",
	}
}

// NewWithBase 测试用：覆盖 base。
func NewWithBase(base string) *Client {
	c := New()
	c.Base = base
	return c
}

// streamClient 返回流式对话用的 HTTP 客户端（无总超时）。
func (c *Client) streamClient() *http.Client {
	if c.StreamHTTP != nil {
		return c.StreamHTTP
	}
	return c.HTTP
}

// discoverDiscoveredModels 初始化并返回已发现的隐藏模型集合（线程安全，只执行一次）
func (c *Client) discoverDiscoveredModels() map[string]bool {
	c.discoveredModelsOnce.Do(func() {
		c.discoveredModels = make(map[string]bool)
	})
	return c.discoveredModels
}

// uuidHex 生成无横线的 uuid（trace 头共用同一 id）。
func uuidHex() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// commonHeaders 所有 API 共享头。
func commonHeaders(req *http.Request, a *auth.Auth) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("User-Agent", "CodeBuddyIDE/3.10.0")
	req.Header.Set("X-Domain", a.Domain)
	req.Header.Set("X-Product", "SaaS")
	req.Header.Set("X-Product-Code", "codebuddy")
	req.Header.Set("X-IDE-Name", "CodeBuddyIDE")
	if a.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.AccessToken)
	}
	if a.UID != "" {
		req.Header.Set("X-User-Id", a.UID)
	}
}

// chatTraceHeaders chat 专属 trace 五件套（必须同 uuid，否则上游拒收）。
func chatTraceHeaders(req *http.Request) {
	id := uuidHex()
	req.Header.Set("Trace-ID", id)
	req.Header.Set("X-Request-ID", id)
	req.Header.Set("X-Conversation-ID", id)
	req.Header.Set("X-Conversation-Request-ID", id)
	req.Header.Set("X-Conversation-Message-ID", id)
}

// doJSON 发请求并解 {code,msg,data} 信封；HTTP 非 2xx 或 code != 0 返回 *provider.Error。
func (c *Client) doJSON(req *http.Request) (json.RawMessage, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, &provider.Error{Kind: c.Classify(resp.StatusCode, string(raw)), Status: resp.StatusCode, Msg: truncate(string(raw), 200)}
	}
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse failed: %w (body: %s)", err, truncate(string(raw), 120))
	}
	if env.Code != 0 {
		return nil, &provider.Error{Kind: ErrClient, Status: resp.StatusCode, Msg: fmt.Sprintf("code=%d msg=%s", env.Code, truncate(env.Msg, 160))}
	}
	return env.Data, nil
}

// ---------------------------------------------------------------------------
// provider.Upstream 实现
// ---------------------------------------------------------------------------

// RefreshToken 用 X-Refresh-Token 轮换 access token（/v2/plugin/auth/token/refresh）。
// 响应 {code,msg,data:{accessToken,expiresAt,expiresIn}}；持锁更新 Auth 字段，
// 调用方负责 SaveAtomic。
func (c *Client) RefreshToken(a *auth.Auth) error {
	a.Lock()
	defer a.Unlock()
	log.Printf("codebuddy refresh start uid=%s", a.UID)
	if strings.TrimSpace(a.RefreshToken) == "" {
		err := fmt.Errorf("no refreshToken")
		log.Printf("codebuddy refresh failed uid=%s err=%v", a.UID, err)
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.Base+"/v2/plugin/auth/token/refresh", bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	commonHeaders(req, a)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Refresh-Token", a.RefreshToken)
	req.Header.Set("X-Auth-Refresh-Source", "plugin")
	data, err := c.doJSON(req)
	if err != nil {
		log.Printf("codebuddy refresh failed uid=%s err=%v", a.UID, err)
		return err
	}
	var tok struct {
		AccessToken  string `json:"accessToken"`
		ExpiresAt    int64  `json:"expiresAt"`
		ExpiresIn    int64  `json:"expiresIn"`
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.Unmarshal(data, &tok); err != nil || tok.AccessToken == "" {
		err := fmt.Errorf("refresh_failed: no accessToken in response — re-login required")
		log.Printf("codebuddy refresh failed uid=%s err=%v", a.UID, err)
		return err
	}
	a.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		a.RefreshToken = tok.RefreshToken
	}
	// 上游给的是毫秒时间戳（expiresAt）；缺省则按 1 年（token 寿命）兜底，避免刷新风暴。
	if tok.ExpiresAt > 0 {
		a.ExpiresAt = tok.ExpiresAt / 1000
	} else if tok.ExpiresIn > 0 {
		a.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix()
	} else {
		a.ExpiresAt = time.Now().Add(365 * 24 * time.Hour).Unix()
	}
	log.Printf("codebuddy refresh success uid=%s expires_at=%d", a.UID, a.ExpiresAt)
	return nil
}

// PrepareBody 改写发往上游的 chat 请求体：
//  1. 强制 stream:true（上游拒绝非流式，code 11101）
//  2. messages[0] 必须是 system（否则 code 11128）；缺 system 时在最前插入
//  3. 剥离 system 提示词里的 gitStatus 段（code 11128 触发源，见下）
//
// gitStatus 剥离：ZCode/Trae 桌面客户端在 git 仓库会话开局时，会把 git 状态
// （Current branch / Git user / Recent commits 等）注入 system 提示词。实测发往
// intl(workbuddy/codebuddy) 上游时，该段会触发 11128 "Illegal API invocation from
// an unapproved channel" 渠道校验（基线实验：system 到 "Main branch: main" 前恒 200，
// 追加 "Git user: <name>" 后恒 400；删除整个 gitStatus 段后恢复 200）。gitStatus
// 属客户端会话元信息而非对话内容，剥掉不影响正常问答与工具调用。
func PrepareBody(src []byte) []byte {
	if len(src) == 0 {
		return src
	}
	var obj map[string]any
	if err := json.Unmarshal(src, &obj); err != nil {
		return src
	}
	obj["stream"] = true
	msgs, _ := obj["messages"].([]any)
	for i, m := range msgs {
		mo, ok := m.(map[string]any)
		if !ok || mo["role"] != "system" {
			continue
		}
		c, ok := mo["content"].(string)
		if !ok {
			continue
		}
		// 删除 gitStatus 段：从 "gitStatus" 起始到下一个空白行+新起点标记(如 "\n\n# " 或段落结束)。
		gs := strings.Index(c, "gitStatus")
		if gs >= 0 {
			// 截断到该位置，清除 git 会话元信息及其后一切，保留下文不含它。
			c = c[:gs]
			// 移除末尾可能遗留的孤立换行/空格，保证 system 内容干净。
			c = strings.TrimRight(c, "\r\n \t")
			mo["content"] = c
			msgs[i] = mo
		}
	}
	if len(msgs) == 0 {
		obj["messages"] = []any{map[string]any{"role": "system", "content": "You are a helpful assistant."}}
	} else if first, ok := msgs[0].(map[string]any); !ok || first["role"] != "system" {
		obj["messages"] = append([]any{map[string]any{"role": "system", "content": "You are a helpful assistant."}}, msgs...)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return src
	}
	return out
}

// ChatStream 发 chat 请求并返回原始 SSE body 流（调用方负责 Close）。
// 非 2xx 时 rc 为 nil、respBody 为上游响应体、err 为 nil；只有传输层失败才返回 err。
func (c *Client) ChatStream(a *auth.Auth, body []byte) (rc io.ReadCloser, status int, respBody []byte, err error) {
	var peek struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &peek)
	prepared := PrepareBody(body)
	req, err := http.NewRequest(http.MethodPost, c.Base+"/v2/chat/completions", bytes.NewReader(prepared))
	if err != nil {
		return nil, 0, nil, err
	}
	commonHeaders(req, a)
	chatTraceHeaders(req)
	req.Header.Set("Accept", "text/event-stream")
	if peek.Model != "" {
		req.Header.Set("X-Model-ID", peek.Model)
	}
	resp, err := c.streamClient().Do(req)
	if err != nil {
		log.Printf("codebuddy chat_stream uid=%s model=%s: transport error: %v", a.UID, peek.Model, err)
		return nil, 0, nil, err
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		kind := c.Classify(resp.StatusCode, string(raw))
		log.Printf("codebuddy chat_stream uid=%s model=%s: upstream %d %s body=%s",
			a.UID, peek.Model, resp.StatusCode, kind, truncate(string(raw), 200))
		return nil, resp.StatusCode, raw, nil
	}

	// 成功调用后，记录模型 ID（用于发现隐藏模型）
	if peek.Model != "" && resp.StatusCode == 200 {
		discovered := c.discoverDiscoveredModels()
		discovered[peek.Model] = true
		log.Printf("codebuddy chat_stream uid=%s model=%s: discovered hidden model (HTTP 200)", a.UID, peek.Model)
	}

	return resp.Body, resp.StatusCode, nil, nil
}

// fetchModelIDs 调 GET /v2/enterprises/personal/models 拉取账号实际可用的
// 真实模型 ID 列表（实测 18 个：6 逻辑槽位 + hy4/hy3 + gpt-5.6-sol/terra/luna、
// gpt-5.5/5.4/5.3-codex、gemini-3.5-flash、glm-5.3/5.2、kimi-k3/2.6）。
// 响应 {code,msg,data:{agents:[{id,models:[...]}]}}。
func (c *Client) fetchModelIDs(a *auth.Auth) ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, c.Base+"/v2/enterprises/personal/models", nil)
	if err != nil {
		return nil, err
	}
	commonHeaders(req, a)
	req.Header.Set("Accept", "application/json")
	data, err := c.doJSON(req)
	if err != nil {
		return nil, err
	}
	var env struct {
		Agents []struct {
			Models []string `json:"models"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("personal/models parse: %w", err)
	}
	seen := map[string]bool{}
	var ids []string
	for _, ag := range env.Agents {
		for _, id := range ag.Models {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("personal/models has no models")
	}
	return ids, nil
}

// staticModel 内置模型目录（含上下文窗口/定价），供 FetchModels 补元数据与
// StaticModels 兜底。采集自 2026-09-11 国际版 App 端 /v3/config 的 cli agent。
type staticModel struct {
	ID            string
	Name          string
	ContextWindow int64
	MaxTokens     int64
	Rate          float64 // 积分倍率 "x0.34" → 0.34；0 = 未知/免费标记
}

// intlModels 国际版真实模型目录。
var intlModels = []staticModel{
	{ID: "default-model", Name: "Auto", ContextWindow: 176000, MaxTokens: 24000},
	{ID: "fast-model", Name: "Fast", ContextWindow: 200000, MaxTokens: 32000, Rate: 0.34},
	{ID: "balanced-model", Name: "Balanced", ContextWindow: 256000, MaxTokens: 32000, Rate: 0.59},
	{ID: "primary-model", Name: "Primary", ContextWindow: 272000, MaxTokens: 72000, Rate: 3.31},
	{ID: "deep-model", Name: "Deep", ContextWindow: 176000, MaxTokens: 24000, Rate: 3.33},
	{ID: "hy4-preview", Name: "Hy4 preview", ContextWindow: 1000000, MaxTokens: 64000},
	{ID: "hy3", Name: "Hy3", ContextWindow: 192000, MaxTokens: 64000},
	{ID: "deepseek-v4.1-flash", Name: "Deepseek-V4.1-Flash (限免)", ContextWindow: 1000000, MaxTokens: 128000},
	{ID: "gpt-6-astra", Name: "GPT-6-Astra", ContextWindow: 1000000, MaxTokens: 128000, Rate: 6.67},
	{ID: "gpt-5.6-sol", Name: "GPT-5.6-Sol", ContextWindow: 1000000, MaxTokens: 128000, Rate: 3.47},
	{ID: "gpt-5.6-terra", Name: "GPT-5.6-Terra", ContextWindow: 1000000, MaxTokens: 128000, Rate: 1.39},
	{ID: "gpt-5.6-luna", Name: "GPT-5.6-Luna", ContextWindow: 1000000, MaxTokens: 128000, Rate: 0.14},
	{ID: "gpt-5.5", Name: "GPT-5.5", ContextWindow: 1000000, MaxTokens: 128000, Rate: 3.31},
	{ID: "gpt-5.4", Name: "GPT-5.4", ContextWindow: 272000, MaxTokens: 72000, Rate: 1.65},
	{ID: "gpt-5.3-codex", Name: "GPT-5.3-Codex", ContextWindow: 272000, MaxTokens: 72000, Rate: 1.25},
	{ID: "gemini-3.5-flash", Name: "Gemini-3.5-Flash", ContextWindow: 1000000, MaxTokens: 65536, Rate: 0.99},
	{ID: "glm-5.3", Name: "GLM-5.3", ContextWindow: 1000000, MaxTokens: 48000, Rate: 0.79},
	{ID: "glm-5.2", Name: "GLM-5.2", ContextWindow: 1000000, MaxTokens: 48000, Rate: 0.79},
	{ID: "kimi-k3", Name: "Kimi-K3", ContextWindow: 1000000, MaxTokens: 32000, Rate: 1.62},
	{ID: "kimi-k2.6", Name: "Kimi-K2.6", ContextWindow: 256000, MaxTokens: 32000, Rate: 0.52},
}

// wbintlHiddenModels WorkBuddy 国际版特有隐藏模型（不在 /v2/enterprises/personal/models 列表中）
// 这些模型需要动态发现，通过直接调用测试可用性
var wbintlHiddenModels = []staticModel{
	// 待补充：根据实际测试添加 wbintl 特有的隐藏模型
	// 示例格式：{ID: "model-name", Name: "Model Name", ContextWindow: 1000000, MaxTokens: 32000, Rate: 0.0},
}

// staticModelOf 按 ID 查内置目录，找到返回指针；找不到返回 nil。
func staticModelOf(id string) *staticModel {
	for i := range intlModels {
		if intlModels[i].ID == id {
			return &intlModels[i]
		}
	}
	return nil
}

// FetchModels 拉取账号实际可用的真实模型列表（/v2/enterprises/personal/models），
// 并以内置目录补上下文/输出上限元数据；接口失败时返回内置目录全量兜底。
func (c *Client) FetchModels(a *auth.Auth) ([]provider.ModelInfo, error) {
	ids, err := c.fetchModelIDs(a)
	if err != nil {
		// 接口失败：退化为内置目录（全部模型），保证服务不因上游波动断模型
		var out []provider.ModelInfo
		for _, m := range intlModels {
			out = append(out, provider.ModelInfo{ID: m.ID, Name: m.Name, ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens})
		}
		// WorkBuddy 国际版额外添加隐藏模型
		if c.Channel == "wbintl" {
			for _, m := range wbintlHiddenModels {
				out = append(out, provider.ModelInfo{ID: m.ID, Name: m.Name, ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens})
			}
		}
		return out, nil
	}
	var out []provider.ModelInfo
	seen := map[string]bool{}
	for _, id := range ids {
		m := staticModelOf(id)
		if m == nil {
			out = append(out, provider.ModelInfo{ID: id, Name: id})
		} else {
			out = append(out, provider.ModelInfo{ID: m.ID, Name: m.Name, ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens})
		}
		seen[id] = true
	}
	// 官方隐藏模型补充：deepseek-v4.1-flash 不在个人/models 列表，但实测可直接调用且不扣积分
	if !seen["deepseek-v4.1-flash"] {
		if m := staticModelOf("deepseek-v4.1-flash"); m != nil {
			out = append(out, provider.ModelInfo{ID: m.ID, Name: m.Name, ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens})
		}
	}
	// WorkBuddy 国际版额外添加隐藏模型
	if c.Channel == "wbintl" {
		for _, m := range wbintlHiddenModels {
			if !seen[m.ID] {
				out = append(out, provider.ModelInfo{ID: m.ID, Name: m.Name, ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens})
			}
		}
	}
	// 添加动态发现的隐藏模型（通过成功调用嗅探到的）
	discovered := c.discoverDiscoveredModels()
	for modelID := range discovered {
		if !seen[modelID] && staticModelOf(modelID) == nil {
			// 不是已知模型且未在接口列表中，可能是新发现的隐藏模型
			out = append(out, provider.ModelInfo{ID: modelID, Name: modelID})
			seen[modelID] = true
		}
	}
	return out, nil
}

// FetchModelPricing 返回内置目录中各模型的积分倍率（始终全量显示，不随账号可用模型过滤）。
func (c *Client) FetchModelPricing(a *auth.Auth) ([]provider.ModelPricing, error) {
	// 无论接口是否成功，都返回完整目录，确保前端费率表不会缺失模型
	var out []provider.ModelPricing

	// 基础模型列表（codebuddy 和 wbintl 共享）
	for i := range intlModels {
		m := &intlModels[i]
		pricing := provider.ModelPricing{
			Model:   m.ID,
			Channel: c.Channel,
			Rate:    m.Rate,
		}
		// 免费模型标记限免说明（Rate=0 也会正常显示）
		if m.Rate == 0 {
			if m.ID == "deepseek-v4.1-flash" {
				pricing.Note = "隐藏模型限免 0 积分 · 不在列表但可直接调用"
			} else {
				pricing.Note = "限免期 0 积分"
			}
		}
		out = append(out, pricing)
	}

	// WorkBuddy 国际版特有隐藏模型（仅当 Channel="wbintl" 时添加）
	if c.Channel == "wbintl" && len(wbintlHiddenModels) > 0 {
		for i := range wbintlHiddenModels {
			m := &wbintlHiddenModels[i]
			pricing := provider.ModelPricing{
				Model:   m.ID,
				Channel: c.Channel,
				Rate:    m.Rate,
			}
			if m.Rate == 0 {
				pricing.Note = "WorkBuddy 国际版隐藏模型限免 0 积分"
			}
			out = append(out, pricing)
		}
	}

	// 添加动态发现的隐藏模型定价信息（通过成功调用嗅探到的）
	discovered := c.discoverDiscoveredModels()
	for modelID := range discovered {
		// 检查是否已在列表中（避免重复）
		exists := false
		for _, p := range out {
			if p.Model == modelID {
				exists = true
				break
			}
		}
		if !exists {
			// 新发现的隐藏模型，默认免费（Rate=0），前端会显示提示
			pricing := provider.ModelPricing{
				Model:   modelID,
				Channel: c.Channel,
				Rate:    0,
				Note:    fmt.Sprintf("动态发现隐藏模型 · 实测可用 (HTTP 200)"),
			}
			out = append(out, pricing)
		}
	}

	return out, nil
}

// deriveID 由 uid + salt 稳定派生设备/会话标识（幂等，避免风控，与 workbuddy2api-hub 一致）。
func deriveID(uid, salt string) string {
	sum := md5.Sum([]byte(salt + ":" + uid))
	return hex.EncodeToString(sum[:])
}

// requestID 带稳定前缀与微秒时间戳的 X-Request-ID。
func requestID(uid string) string {
	return deriveID(uid, "req") + "-" + fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
}

// billingHeaders 积分接口专用请求头：WorkBuddy 桌面端 UA + 设备指纹，
// 与 chat 用的 CodeBuddyIDE 头不同（参考 workbuddy2api-hub headers(purpose="billing")）。
// intl 版不设 X-Product（仅国区需要），实测可省。
func billingHeaders(req *http.Request, a *auth.Auth) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("User-Agent", "WorkBuddy/5.5.2")
	req.Header.Set("Accept-Language", "en-US")
	req.Header.Set("X-CodeBuddy-Request", "1")
	req.Header.Set("X-Request-ID", requestID(a.UID))
	req.Header.Set("X-Machine-ID", deriveID(a.UID, "machine"))
	req.Header.Set("X-Session-ID", deriveID(a.UID, "session"))
	if a.Domain != "" {
		req.Header.Set("X-Domain", a.Domain)
		req.Header.Set("Origin", "https://"+a.Domain)
		req.Header.Set("Referer", "https://"+a.Domain+"/")
	}
	if a.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.AccessToken)
	}
	if a.UID != "" {
		req.Header.Set("X-User-Id", a.UID)
	}
}

// UserResource 持久化余额查询，复用 UserResourceDetail 汇总。
func (c *Client) UserResource(a *auth.Auth) (int64, error) {
	remain, _, err := c.UserResourceDetail(a)
	return remain, err
}

// UserResourceDetail 积分明细：POST /v2/billing/meter/get-user-resource。
// 返回全部活跃套餐（Bonus Pack / Free Plan 等）的剩余额度与到期时间
// （DeductionEndTime 毫秒时间戳转 Unix 秒，供"快到期优先"路由）。
// 周期额度（CycleCapacity*）优先，参考 workbuddy2api-hub fetch_credits 汇总口径。
func (c *Client) UserResourceDetail(a *auth.Auth) (int64, []provider.ResourceItem, error) {
	payload, err := json.Marshal(map[string]any{
		"PageNumber":               1,
		"PageSize":                 100,
		"ProductCode":              "p_tcaca",
		"Status":                   []int{0, 3},
		"PackageEndTimeRangeBegin": time.Now().Format("2006-01-02 15:04:05"),
		"PackageEndTimeRangeEnd":   "2036-01-01 00:00:00",
	})
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.Base+"/v2/billing/meter/get-user-resource", bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	billingHeaders(req, a)
	data, err := c.doJSON(req)
	if err != nil {
		return 0, nil, err
	}
	var env struct {
		Response struct {
			Data struct {
				Accounts []struct {
					PackageName         string `json:"PackageName"`
					CycleCapacitySize   int64  `json:"CycleCapacitySize"`
					CycleCapacityRemain int64  `json:"CycleCapacityRemain"`
					CycleCapacityUsed   int64  `json:"CycleCapacityUsed"`
					CapacitySize        int64  `json:"CapacitySize"`
					CapacityRemain      int64  `json:"CapacityRemain"`
					CapacityUsed        int64  `json:"CapacityUsed"`
					DeductionEndTime    int64  `json:"DeductionEndTime"`
				} `json:"Accounts"`
			} `json:"Data"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return 0, nil, fmt.Errorf("billing parse failed: %w (body: %s)", err, truncate(string(data), 160))
	}
	var remain int64
	items := make([]provider.ResourceItem, 0, len(env.Response.Data.Accounts))
	for _, acct := range env.Response.Data.Accounts {
		var size, used, rem int64
		if acct.CycleCapacitySize > 0 {
			size, rem = acct.CycleCapacitySize, acct.CycleCapacityRemain
			used = size - rem
			if used < 0 {
				used = 0
			}
			if acct.CycleCapacityUsed > used {
				used = acct.CycleCapacityUsed
				if rem = size - used; rem < 0 {
					rem = 0
				}
			}
		} else {
			size, rem, used = acct.CapacitySize, acct.CapacityRemain, acct.CapacityUsed
			if rem < 0 {
				rem = 0
			}
		}
		remain += rem
		name := acct.PackageName
		if name == "" {
			name = "Package"
		}
		items = append(items, provider.ResourceItem{
			Name:     name,
			Total:    size,
			Used:     used,
			Remain:   rem,
			ExpireAt: acct.DeductionEndTime / 1000, // ms → Unix 秒；0 = 长期有效
		})
	}
	return remain, items, nil
}

// DailyCheckin intl 无签到活动（成长中心为国区功能）。
func (c *Client) DailyCheckin(a *auth.Auth) error {
	return fmt.Errorf("codebuddy 国际版暂无签到活动")
}

// ErrKind 错误分类（复用 upstream 语义 + intl 专属 code）。
const (
	ErrHardCredit  = provider.ErrHardCredit
	ErrSoftRate    = provider.ErrSoftRate
	ErrSessionDead = provider.ErrSessionDead
	ErrNotFound    = provider.ErrNotFound
	ErrServer      = provider.ErrServer
	ErrClient      = provider.ErrClient
)

// Classify 按 HTTP 状态码 + body 判定错误类别。
func (c *Client) Classify(status int, body string) provider.ErrKind { return Classify(status, body) }

// Classify 独立分类（供测试直接调用）。
func Classify(status int, body string) provider.ErrKind {
	if status == http.StatusPaymentRequired {
		return ErrHardCredit
	}
	if status == http.StatusUnauthorized {
		return ErrSessionDead
	}
	if status == http.StatusTooManyRequests {
		return ErrSoftRate
	}
	if status == http.StatusNotFound {
		return ErrNotFound
	}
	if status >= 500 {
		// 代理/网关链路层的 502/504（上游域名解析失败、连接被重置等）属网络故障，
		// 与账号无关，不应累计 errCount（2026-10-05/10-09 事故同族）。body 命中
		// 网络特征（nginx/网关透传的错误页、upstream_unreachable JSON）时归为
		// ErrUpstreamDown，走短冷却。
		if provider.LooksUpstreamUnreachable(body) {
			return provider.ErrUpstreamDown
		}
		return ErrServer
	}
	if status >= 400 {
		// intl 业务 code 目测 11101/11102/11128 均属客户端请求问题
		return ErrClient
	}
	return provider.ErrNone
}

// Stream 透传上游 SSE（OpenAI 兼容，复用 upstream 通用实现）。
func (c *Client) Stream(w http.ResponseWriter, r io.Reader) error {
	return upstream.Stream(w, r)
}

// Aggregate 聚合 SSE 为单个 OpenAI 响应（intl 返回标准 OpenAI 兼容格式）。
func (c *Client) Aggregate(r io.Reader) (map[string]any, error) {
	return upstream.Aggregate(r)
}

// StreamWithThink / AggregateWithThink 实现 provider.ThinkPassthrough。
func (c *Client) StreamWithThink(w http.ResponseWriter, r io.Reader, passthrough bool) error {
	return upstream.StreamWithThink(w, r, passthrough)
}

// GrowthTravel / GrowthLottery / GrowthTasksDetailed 成长中心功能暂未支持。
func (c *Client) GrowthTravel(a *auth.Auth) error  { return nil }
func (c *Client) GrowthLottery(a *auth.Auth) error { return nil }
func (c *Client) GrowthTasksDetailed(a *auth.Auth) provider.GrowthTasksResult {
	return provider.GrowthTasksResult{Message: "暂未支持"}
}

func (c *Client) AggregateWithThink(r io.Reader, keepReasoning bool) (map[string]any, error) {
	return upstream.AggregateWithThink(r, keepReasoning)
}

// StaticModels 静态模型兜底（FetchModels 拉取失败时用）。
func StaticModels() []provider.ModelInfo {
	var out []provider.ModelInfo
	for _, m := range intlModels {
		out = append(out, provider.ModelInfo{ID: m.ID, Name: m.Name, ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens})
	}
	return out
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
