// client.go QoderWork 上游客户端：业务 API（dt- Bearer）+
// COSY 签名对话转发 + 错误分类，实现 provider.Upstream 接口。
package qoder

import (
	"bytes"
	"crypto/md5"
	"crypto/tls"
	"encoding/base64"
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
)

// Client QoderWork 上游客户端。
type Client struct {
	HTTP    *http.Client
	Base    string // 业务 API，默认 https://openapi.qoder.com.cn
	Gateway string // 推理网关，默认 https://gateway.qoder.com.cn

	// StreamHTTP 供流式对话使用：无总超时，思考型模型可长时间输出。
	// http.Client.Timeout 覆盖响应体读取，会把长思考的 SSE 硬掐断。nil 时回退到 HTTP。
	StreamHTTP *http.Client

	// modelMap 客户端名（display_name 规范化）→ 上游 model key。
	// 由 FetchModels 填充；ChatStream 优先查此表，查不到再查静态表。
	modelMu  sync.RWMutex
	modelMap map[string]string

	// lastModel 最近一次 chat 请求的客户端模型名（含 qoder/ 前缀），
	// 供 Aggregate/Stream 覆盖响应中的 model 字段（上游恒为 auto）。
	lastModelMu sync.RWMutex
	lastModel   string
}

// New 生产默认。Qoder gateway 对 HTTP/2 不友好（stream INTERNAL_ERROR），强制 HTTP/1.1。
func New() *Client {
	return NewWithTimeout(180 * time.Second)
}

// NewWithTimeout 指定上游 HTTP 超时；配置连接池。
func NewWithTimeout(timeout time.Duration) *Client {
	tr := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 120 * time.Second,                                      // 流式无总超时，靠它兜住建连卡死
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{}, // 强制 HTTP/1.1
	}
	return &Client{
		HTTP:       &http.Client{Timeout: timeout, Transport: tr},
		StreamHTTP: &http.Client{Transport: tr},
		Base:       OpenAPIBase,
		Gateway:    GatewayBase,
	}
}

// streamClient 返回流式对话用的 HTTP 客户端（无总超时）。
func (c *Client) streamClient() *http.Client {
	if c.StreamHTTP != nil {
		return c.StreamHTTP
	}
	return c.HTTP
}

// NewWithBase 测试用：覆盖 base/gateway。
func NewWithBase(base, gateway string) *Client {
	c := New()
	c.Base = base
	c.Gateway = gateway
	return c
}

// setModelMap 记录动态模型映射（客户端名 → key）。
func (c *Client) setModelMap(m map[string]string) {
	c.modelMu.Lock()
	c.modelMap = m
	c.modelMu.Unlock()
}

// setLastModel 记录最近一次 chat 的客户端模型名。
func (c *Client) setLastModel(m string) {
	c.lastModelMu.Lock()
	c.lastModel = m
	c.lastModelMu.Unlock()
}

// lastClientModel 读取最近一次 chat 的客户端模型名（含前缀）。
func (c *Client) lastClientModel() string {
	c.lastModelMu.RLock()
	defer c.lastModelMu.RUnlock()
	return c.lastModel
}

// modelKey 客户端模型名 → 上游 model key：动态映射优先，静态表兜底。
func (c *Client) modelKey(clientName string) string {
	c.modelMu.RLock()
	k := c.modelMap[clientName]
	c.modelMu.RUnlock()
	if k != "" {
		return k
	}
	return staticModelKeys[clientName]
}

func (c *Client) gatewayBase() string { return c.Gateway }
func (c *Client) base() string        { return c.Base }

func billingHeaders(req *http.Request, dt string) {
	req.Header.Set("Authorization", "Bearer "+dt)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
}

// desktopHeaders 构造活动平台（/sash/...）出站头。
//
// 来源与实测修正（2026-10-01，本机头矩阵实测，4 个 qoder 账号）：
//   - 基线：qoder2api-hub v1.1.1（issue #1）指出缺 Cosy-Version 时上游静默返回
//     空活动列表，故必须带 cosy-clienttype=10 + cosy-version。
//   - 修正：**必须省略 cosy-machinetoken**。参考仓库用官方 runtime-info.exe 取的是
//     真设备 token；我们只能由 uid 派生假值，服务端比对不符 → 判定为异设备，
//     把设备定向的 CLAIM_BENEFIT 活动静默过滤掉（表现为"无可用签到活动"）。
//     A/B 实测：带上它 → campaigns 只剩 VIEW_DETAILS 推广；去掉它 → 恢复
//     act-20260930-125（CLAIM_BEFIT/100 Credits/CLAIMABLE）并领取成功。
//     其余 machine 系列头（id/type/code/os/hostname）经实测无副作用，保留以贴近桌面端。
func desktopHeaders(req *http.Request, dt string, a *auth.Auth) {
	billingHeaders(req, dt)
	req.Header.Set("accept-language", "zh-CN")
	req.Header.Set("user-agent", "Qoder")
	req.Header.Set("cosy-clienttype", DesktopClientType) // 桌面端（CLI=5 / QoderWork=6）
	req.Header.Set("cosy-version", DesktopClientVersion)
	req.Header.Set("cosy-machineid", a.MachineID)
	// 故意不设 cosy-machinetoken：派生假值会让上游过滤掉设备定向活动（见上）。
	req.Header.Set("cosy-machinetype", a.MachineType)
	req.Header.Set("cosy-machinecode", machineCode(a))
	req.Header.Set("cosy-machineos", desktopMachineOS)
	req.Header.Set("cosy-machinehostname", desktopMachineHostname)
}

// machineCode 由 uid 稳定派生（32 位十六进制 md5），与 qoder2api-hub
// derive_id(uid, "machinecode") 同构。账号级稳定，避免随机值触发上游风控。
func machineCode(a *auth.Auth) string {
	sum := md5.Sum([]byte("machinecode:" + a.UID))
	return hex.EncodeToString(sum[:])
}

func (c *Client) get(path, dt string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, c.base()+path, nil)
	if err != nil {
		return nil, err
	}
	billingHeaders(req, dt)
	return c.HTTP.Do(req)
}

func (c *Client) post(path, dt string, body []byte) (*http.Response, error) {
	if body == nil {
		body = []byte("{}")
	}
	req, err := http.NewRequest(http.MethodPost, c.base()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	billingHeaders(req, dt)
	return c.HTTP.Do(req)
}

func readBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// ---------------------------------------------------------------------------
// provider.Upstream 实现
// ---------------------------------------------------------------------------

// RefreshToken 用 drt- 走 /api/v1/deviceToken/refresh 轮换 dt/drt。
// 401/403 → ErrSessionDead（需重新 OAuth 登录）。
func (c *Client) RefreshToken(a *auth.Auth) error {
	a.Lock()
	defer a.Unlock()
	log.Printf("qoder refresh start uid=%s", a.UID)
	if strings.TrimSpace(a.RefreshToken) == "" {
		err := fmt.Errorf("no drt- available")
		log.Printf("qoder refresh failed uid=%s err=%v", a.UID, err)
		return err
	}
	body, _ := json.Marshal(map[string]string{"refresh_token": a.RefreshToken})
	req, err := http.NewRequest(http.MethodPost, c.base()+EpDTRefresh, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// refresh 用独立短超时客户端
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("qoder refresh failed uid=%s err=%v", a.UID, err)
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// drt 失效 → 需重新登录
		err := &provider.Error{Kind: provider.ErrSessionDead, Status: resp.StatusCode, Msg: truncate(string(raw), 200)}
		log.Printf("qoder refresh session-dead uid=%s err=%v", a.UID, err)
		return err
	}
	if resp.StatusCode >= 400 {
		err := fmt.Errorf("deviceToken refresh http %d: %s", resp.StatusCode, truncate(string(raw), 200))
		log.Printf("qoder refresh failed uid=%s err=%v", a.UID, err)
		return err
	}
	var out struct {
		Token        string `json:"token"`
		DeviceToken  string `json:"device_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    string `json:"expires_at"`
		ExpiresIn    int64  `json:"expires_in"` // ms
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		log.Printf("qoder refresh parse failed uid=%s err=%v", a.UID, err)
		return fmt.Errorf("deviceToken refresh parse: %w", err)
	}
	dt := out.Token
	if dt == "" {
		dt = out.DeviceToken
	}
	if dt == "" || out.RefreshToken == "" {
		return fmt.Errorf("deviceToken refresh: incomplete token pair")
	}
	a.AccessToken = dt
	a.RefreshToken = out.RefreshToken
	now := time.Now()
	if out.ExpiresIn > 0 {
		a.ExpiresAt = now.Add(time.Duration(out.ExpiresIn) * time.Millisecond).Unix()
	} else if out.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, out.ExpiresAt); err == nil {
			a.ExpiresAt = t.Unix()
		}
	}
	if a.ExpiresAt == 0 {
		a.ExpiresAt = now.Add(30 * 24 * time.Hour).Unix() // dt 观测寿命 30d
	}
	log.Printf("qoder refresh success uid=%s expires_at=%d", a.UID, a.ExpiresAt)
	return nil
}

// parse10605 解析 Qoder 网关并发限流/排队错误（10605 与 retryAfterSeconds）。
func parse10605(body []byte) (retryAfter int, ok bool) {
	if !bytes.Contains(body, []byte("10605")) && !bytes.Contains(body, []byte("retryAfterSeconds")) {
		return 0, false
	}
	var top struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &top); err == nil {
		var inner struct {
			RetryAfterSeconds int `json:"retryAfterSeconds"`
		}
		if err := json.Unmarshal([]byte(top.Message), &inner); err == nil && inner.RetryAfterSeconds > 0 {
			return inner.RetryAfterSeconds, true
		}
	}
	var direct struct {
		RetryAfterSeconds int `json:"retryAfterSeconds"`
	}
	if err := json.Unmarshal(body, &direct); err == nil && direct.RetryAfterSeconds > 0 {
		return direct.RetryAfterSeconds, true
	}
	return 2, true
}

// ChatStream 发 chat 请求并返回原始嵌套 SSE body 流（调用方负责 Close）。
// 非 2xx 时 rc 为 nil、respBody 为上游响应体、err 为 nil；只有传输层失败才返回 err。
func (c *Client) ChatStream(a *auth.Auth, body []byte) (rc io.ReadCloser, status int, respBody []byte, err error) {
	// body 是 server 侧改写后的 OpenAI 请求；qoder 需要 messages/model/tools
	// 及采样/思考参数（透传到上游顶层 parameters）。
	var reqOpenAI struct {
		Model           string           `json:"model"`
		Messages        []map[string]any `json:"messages"`
		Tools           []any            `json:"tools"`
		ReasoningEffort string           `json:"reasoning_effort"`
		Temperature     *float64         `json:"temperature"`
		TopP            *float64         `json:"top_p"`
		MaxTokens       *int             `json:"max_tokens"`
		Stop            any              `json:"stop"`
	}
	if err := json.Unmarshal(body, &reqOpenAI); err != nil {
		return nil, 0, nil, fmt.Errorf("parse chat body: %w", err)
	}
	c.setLastModel(reqOpenAI.Model)
	modelKey := c.modelKey(reqOpenAI.Model)
	if modelKey == "" {
		modelKey = reqOpenAI.Model
	}

	opts := ChatOptions{
		ReasoningEffort: reqOpenAI.ReasoningEffort,
		Temperature:     reqOpenAI.Temperature,
		TopP:            reqOpenAI.TopP,
		MaxTokens:       reqOpenAI.MaxTokens,
		Stop:            reqOpenAI.Stop,
	}

	rawBody, err := buildAgentBody(reqOpenAI.Messages, modelKey, reqOpenAI.Tools, opts)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("build qoder body: %w", err)
	}
	encoded := qoderEncode(rawBody)
	url := c.gatewayBase() + EpChat
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(encoded))
	if err != nil {
		return nil, 0, nil, err
	}
	dt := a.JWT()
	sess, err := NewCosySession(a.MachineID, a.MachineToken, a.MachineType, a.Nickname, a.UID, dt, a.RefreshToken)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("cosy session: %w", err)
	}
	if err := sess.ApplyHeaders(req, encoded, url, a.UID, true, modelKey); err != nil {
		return nil, 0, nil, fmt.Errorf("cosy headers: %w", err)
	}
	resp, err := c.streamClient().Do(req)
	if err != nil {
		log.Printf("qoder chat_stream uid=%s model=%s: transport error: %v", a.UID, modelKey, err)
		return nil, 0, nil, err
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		log.Printf("qoder chat_stream uid=%s model=%s: upstream %d body=%s",
			a.UID, modelKey, resp.StatusCode, truncate(string(raw), 200))

		// 10605 自动排队退避原地重试（Qoder 突发并发限流通常仅需退避 2 秒）
		if retryAfter, ok := parse10605(raw); ok && retryAfter <= 3 {
			log.Printf("qoder chat_stream uid=%s model=%s: hit 10605 rate limit, waiting %ds for in-situ retry...",
				a.UID, modelKey, retryAfter)
			time.Sleep(time.Duration(retryAfter) * time.Second)

			req2, err2 := http.NewRequest(http.MethodPost, url, strings.NewReader(encoded))
			if err2 == nil {
				if err2 = sess.ApplyHeaders(req2, encoded, url, a.UID, true, modelKey); err2 == nil {
					resp2, err2 := c.HTTP.Do(req2)
					if err2 == nil {
						if resp2.StatusCode < 400 {
							log.Printf("qoder chat_stream uid=%s model=%s: in-situ retry succeeded!", a.UID, modelKey)
							return resp2.Body, resp2.StatusCode, nil, nil
						}
						raw2, _ := io.ReadAll(io.LimitReader(resp2.Body, 1<<20))
						resp2.Body.Close()
						log.Printf("qoder chat_stream uid=%s model=%s: in-situ retry failed with status %d body=%s",
							a.UID, modelKey, resp2.StatusCode, truncate(string(raw2), 200))
						return nil, resp2.StatusCode, raw2, nil
					}
				}
			}
		}

		return nil, resp.StatusCode, raw, nil
	}
	return resp.Body, resp.StatusCode, nil, nil
}

// UserResource 查询账号当前可花费积分（基础 + 赠送聚合）。
func (c *Client) UserResource(a *auth.Auth) (int64, error) {
	dt := a.JWT()
	if dt == "" {
		return 0, fmt.Errorf("no dt- available")
	}
	resp, err := c.get(EpQuotaUsage, dt)
	if err != nil {
		return 0, err
	}
	raw, err := readBody(resp)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode >= 400 {
		return 0, c.classifyError(resp.StatusCode, string(raw))
	}
	var q struct {
		UserQuota struct {
			Remaining float64 `json:"remaining"`
		} `json:"userQuota"`
		AddOnQuota struct {
			Remaining float64 `json:"remaining"`
		} `json:"addOnQuota"`
		IsQuotaExceeded bool `json:"isQuotaExceeded"`
	}
	if err := json.Unmarshal(raw, &q); err != nil {
		return 0, fmt.Errorf("quota parse: %w", err)
	}
	return int64(q.UserQuota.Remaining + q.AddOnQuota.Remaining), nil
}

// UserResourceDetail 查询积分明细：userQuota + addOnQuota 两个条目。
func (c *Client) UserResourceDetail(a *auth.Auth) (int64, []provider.ResourceItem, error) {
	dt := a.JWT()
	if dt == "" {
		return 0, nil, fmt.Errorf("no dt- available")
	}
	resp, err := c.get(EpQuotaUsage, dt)
	if err != nil {
		return 0, nil, err
	}
	raw, err := readBody(resp)
	if err != nil {
		return 0, nil, err
	}
	if resp.StatusCode >= 400 {
		return 0, nil, c.classifyError(resp.StatusCode, string(raw))
	}
	var q struct {
		ExpiresAt int64 `json:"expiresAt"` // 毫秒时间戳；同期 quota 响应实测携带
		UserQuota struct {
			Total     float64 `json:"total"`
			Used      float64 `json:"used"`
			Remaining float64 `json:"remaining"`
		} `json:"userQuota"`
		AddOnQuota struct {
			Total     float64 `json:"total"`
			Used      float64 `json:"used"`
			Remaining float64 `json:"remaining"`
		} `json:"addOnQuota"`
	}
	if err := json.Unmarshal(raw, &q); err != nil {
		return 0, nil, fmt.Errorf("quota parse: %w", err)
	}
	expire := normalizeExpireMs(q.ExpiresAt)
	total := int64(q.UserQuota.Remaining + q.AddOnQuota.Remaining)
	items := []provider.ResourceItem{
		{Name: "用户套餐", Total: int64(q.UserQuota.Total), Used: int64(q.UserQuota.Used), Remain: int64(q.UserQuota.Remaining), ExpireAt: expire},
	}
	if q.AddOnQuota.Total > 0 || q.AddOnQuota.Remaining > 0 {
		items = append(items, provider.ResourceItem{Name: "赠送额度", Total: int64(q.AddOnQuota.Total), Used: int64(q.AddOnQuota.Used), Remain: int64(q.AddOnQuota.Remaining), ExpireAt: expire})
	}
	return total, items, nil
}

// normalizeExpireMs 归一化上游到期时间戳：≥1e12 视为毫秒（qoder 实测该形态）除 1000 → Unix 秒；
// 0 及未识别返回 0（=长期）。上游用"永不到期"哨兵值（实测 9999-12-31 = 253402214400s，
// 面板曾显示成"2912169 天后到期"），超过 2100 年的一律归 0（=长期有效）。
func normalizeExpireMs(ts int64) int64 {
	if ts <= 0 {
		return 0
	}
	if ts >= 1e12 {
		ts /= 1000
	}
	if ts >= 4102444800 { // 2100-01-01 UTC
		return 0
	}
	return ts
}

// DailyCheckin Qoder 每日签到（campaigns 活动流程，每日 100 Credits）。
// 链路照抄 qoder2api-hub v1.1.1（桌面端抓包还原）：GET campaigns 找 CLAIMABLE 的
// CLAIM_BENEFIT 活动 → POST {id}/claim 空 body 领取。鉴权仅需 dt- Bearer，无需 COSY
// 签名，但**必须带桌面端请求头**（见 desktopHeaders）——缺 Cosy-Version 时服务端静默
// 返回空活动列表；同时**不可带 cosy-machinetoken**，否则设备定向活动被过滤。
// claim 幂等：已领返回 replayed=true。
// 已领取时返回"已签到"错误（scheduler isAlready 识别为成功状态）。
func (c *Client) DailyCheckin(a *auth.Auth) error {
	dt := a.JWT()
	if dt == "" {
		return fmt.Errorf("no dt- available")
	}
	EnsureFingerprint(a)
	headers := func(req *http.Request) {
		desktopHeaders(req, dt, a)
	}

	// Step 1: 查询活动列表
	req, err := http.NewRequest(http.MethodGet, c.base()+EpCampaigns, nil)
	if err != nil {
		return err
	}
	headers(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	raw, err := readBody(resp)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return c.classifyError(resp.StatusCode, string(raw))
	}
	var list struct {
		Campaigns []struct {
			CampaignID  string `json:"campaignId"`
			CampaignKey string `json:"campaignKey"`
			ActionType  string `json:"actionType"`
			ClaimStatus string `json:"claimStatus"`
		} `json:"campaigns"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return fmt.Errorf("campaigns parse: %w", err)
	}
	target := ""
	targetKey := ""
	already := false
	for _, cp := range list.Campaigns {
		if cp.ActionType != "CLAIM_BENEFIT" {
			continue
		}
		switch cp.ClaimStatus {
		case "CLAIMABLE":
			if target == "" {
				target, targetKey = cp.CampaignID, cp.CampaignKey
			}
		case "CLAIMED":
			already = true
		}
	}
	if target == "" {
		if already {
			log.Printf("qoder checkin already uid=%s", a.UID)
			return fmt.Errorf("已签到")
		}
		return fmt.Errorf("无可用签到活动")
	}

	// Step 2: 领取（空 body，抓包确认；POST 需带 origin）
	claimPath := fmt.Sprintf(EpCampaignClaim, target)
	req, err = http.NewRequest(http.MethodPost, c.base()+claimPath, nil)
	if err != nil {
		return err
	}
	headers(req)
	req.Header.Set("origin", "https://openapi.qoder.com.cn")
	resp, err = c.HTTP.Do(req)
	if err != nil {
		return err
	}
	raw, err = readBody(resp)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return c.classifyError(resp.StatusCode, string(raw))
	}
	var claim struct {
		Status      string `json:"status"`
		Replayed    bool   `json:"replayed"`
		FailureCode string `json:"failureCode"`
		Benefit     *struct {
			Kind   string `json:"kind"`
			Amount int    `json:"amount"`
		} `json:"benefit"`
	}
	if err := json.Unmarshal(raw, &claim); err != nil {
		return fmt.Errorf("claim parse: %w", err)
	}
	switch {
	case claim.Status == "CLAIMED" && !claim.Replayed:
		amount := 0
		if claim.Benefit != nil {
			amount = claim.Benefit.Amount
		}
		log.Printf("qoder checkin claimed uid=%s campaign=%s amount=%d", a.UID, targetKey, amount)
		return nil
	case claim.Status == "CLAIMED" && claim.Replayed:
		log.Printf("qoder checkin already(replayed) uid=%s", a.UID)
		return fmt.Errorf("已签到")
	case strings.EqualFold(claim.FailureCode, "SAME_PERSON_ALREADY_CLAIMED"):
		// 上游按"人"去重（同机器指纹多号合并为一人），共享每轮一次额度。
		// 参考 qoder2api-hub：第二个号返回 BLOCKED + 该 failureCode 且列表里隐藏活动。
		log.Printf("qoder checkin blocked(same-person) uid=%s", a.UID)
		return fmt.Errorf("已签到（同设备其他账号已领取本轮）")
	default:
		return fmt.Errorf("claim status=%s code=%s", claim.Status, claim.FailureCode)
	}
}

// Classify 实现 provider.Upstream。
func (c *Client) Classify(status int, body string) provider.ErrKind { return Classify(status, body) }

// Stream 实现 provider.Upstream（嵌套 SSE → 标准 OpenAI SSE 透传）。
func (c *Client) Stream(w http.ResponseWriter, r io.Reader) error {
	return Stream(w, r, c.lastClientModel())
}

// Aggregate 实现 provider.Upstream（嵌套 SSE 聚合）。
func (c *Client) Aggregate(r io.Reader) (map[string]any, error) {
	return aggregate(r, c.lastClientModel())
}

// StreamWithThink / AggregateWithThink 实现 provider.ThinkPassthrough。
// qoder 的 OpenAI 化输出本就原样保留 reasoning_content（不剥离），
// 两个变体直接复用原实现。
func (c *Client) StreamWithThink(w http.ResponseWriter, r io.Reader, passthrough bool) error {
	return Stream(w, r, c.lastClientModel())
}

func (c *Client) AggregateWithThink(r io.Reader, keepReasoning bool) (map[string]any, error) {
	return aggregate(r, c.lastClientModel())
}

// ---------------------------------------------------------------------------
// 错误分类
// ---------------------------------------------------------------------------

var hardMarkers = []string{
	"insufficient credit", "no credit", "credit exhausted", "out of credit",
	"quota exceeded", "quota exhaust", "payment required", "credit not enough",
	"not enough credit", "isquotaexceeded\":true",
	"积分不足", "额度不足", "余额不足", "积分用完", "额度用尽", "没有积分",
}

// Classify 按 HTTP 状态码 + body 判定错误类别。
func Classify(status int, body string) provider.ErrKind {
	// Qoder 10605 并发排队与限流（上游返回 403，body 含 10605 或 retryAfterSeconds）
	if strings.Contains(body, "10605") || strings.Contains(body, "retryAfterSeconds") {
		return provider.ErrSoftRate
	}
	if status == http.StatusPaymentRequired {
		return provider.ErrHardCredit
	}
	lower := strings.ToLower(body)
	for _, m := range hardMarkers {
		if strings.Contains(lower, strings.ToLower(m)) || strings.Contains(body, m) {
			return provider.ErrHardCredit
		}
	}
	// TOKEN_EXPIRE 优先于通用 401
	if status == http.StatusUnauthorized && strings.Contains(body, "TOKEN_EXPIRE") {
		return provider.ErrSessionDead // 无有效 dt → 需重新登录（dt 刷新由 RefreshToken 处理）
	}
	if status == http.StatusUnauthorized {
		return provider.ErrSessionDead
	}
	if status == http.StatusTooManyRequests {
		return provider.ErrSoftRate
	}
	if status == http.StatusNotFound {
		return provider.ErrNotFound
	}
	if status >= 500 {
		// body 呈网络层故障（DNS/连接/读写）→ 上游不可达，非账号问题
		if provider.LooksUpstreamUnreachable(body) {
			return provider.ErrUpstreamDown
		}
		return provider.ErrServer
	}
	if status >= 400 {
		return provider.ErrClient
	}
	return provider.ErrNone
}

// classifyError 转成 provider.Error。
func (c *Client) classifyError(status int, body string) error {
	return &provider.Error{Kind: Classify(status, body), Status: status, Msg: truncate(body, 200)}
}

// EnsureFingerprint 为账号生成/保持 COSY 机器指纹（MachineID/Token/Type）。
// 幂等：已有则保留。与 qoderwork2api EnsureMachineFingerprint 对齐。
func EnsureFingerprint(a *auth.Auth) {
	if a.MachineID == "" {
		a.MachineID = uuid4()
	}
	if a.MachineToken == "" {
		seed := []byte(uuid4() + uuid4())
		if len(seed) > 50 {
			seed = seed[:50]
		}
		a.MachineToken = base64.RawURLEncoding.EncodeToString(seed)
	}
	if a.MachineType == "" {
		a.MachineType = strings.ReplaceAll(uuid4(), "-", "")[:18]
	}
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// GrowthTravel / GrowthLottery / GrowthTasksDetailed 成长中心功能暂未支持。
func (c *Client) GrowthTravel(a *auth.Auth) error  { return nil }
func (c *Client) GrowthLottery(a *auth.Auth) error { return nil }
func (c *Client) GrowthTasksDetailed(a *auth.Auth) provider.GrowthTasksResult {
	return provider.GrowthTasksResult{Message: "暂未支持"}
}
