// Package provider 定义不同上游（workbuddy / traework）共用的最小接口。
// 只抽取 server/scheduler 必需能力，避免为未来平台过度设计。
package provider

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"zerogate/internal/auth"
)

// Kind 平台标识，同时也是模型名前缀。
type Kind string

const (
	WorkBuddy Kind = "workbuddy"
	TraeWork  Kind = "traework"
	Qoder     Kind = "qoder"
	CodeBuddy Kind = "codebuddy" // 国际版 www.codebuddy.ai
	WBIntl    Kind = "wbintl"    // WorkBuddy 国际版 www.workbuddy.ai（同后端，独立入口）
	ZCode     Kind = "zcode"     // 智谱 ZCode (Z.AI) https://zcode.z.ai
	AutoClaw  Kind = "autoclaw"  // AutoClaw（Z.ai 官方代理通道）
	Atria     Kind = "atria"     // Atria Dawn Preview（上海人工智能实验室）https://api.atria-asi.ai
	Copilot   Kind = "copilot"   // 本机白嫖服务 127.0.0.1:4141（OpenAI 兼容）
	Doubao    Kind = "doubao"    // 本机白嫖服务 127.0.0.1:7868（OpenAI 兼容）
	GptGemini Kind = "gptgemini" // 本机白嫖服务 127.0.0.1:8317（OpenAI 兼容）
	GlmFlash  Kind = "glmflash"  // 智谱开放平台 Flash 家族（永久免费层，OpenAI 兼容）
	Groq      Kind = "groq"      // Groq 免费层（Llama/Qwen 推理云，OpenAI 兼容）
	Tierflow  Kind = "tierflow"  // tierflow.cn 免费聚合（OpenAI 兼容，Bearer Key）
)

func (k Kind) String() string { return string(k) }

// ErrKind 错误分类，驱动 pool 冷却状态机。
type ErrKind int

const (
	ErrNone        ErrKind = iota // 成功
	ErrHardCredit                 // 余额/权益不足 → 长冷却
	ErrSoftRate                   // 429 软限流 → 短冷却
	ErrSessionDead                // 登录态失效 → 禁用
	ErrNotFound                   // 404 上游偶发 → 短冷却不累计 errCount
	ErrServer                     // 5xx 上游故障
	ErrClient                     // 其他 4xx / 业务错误
	ErrBanned                     // 封禁（如 410004）→ 24h 长冷却，重复 3 次转禁用
	ErrBridgeDown                 // 本地桥（127.0.0.1）连不上 → 非账号问题，短冷却不累计
	ErrUpstreamDown               // 桥活着但上游域名/网络不可达（DNS 失败等）→ 非账号问题，短冷却不累计
)

func (k ErrKind) String() string {
	switch k {
	case ErrHardCredit:
		return "hard_credit"
	case ErrSoftRate:
		return "soft_rate"
	case ErrSessionDead:
		return "session_dead"
	case ErrNotFound:
		return "not_found"
	case ErrServer:
		return "server"
	case ErrBanned:
		return "banned"
	case ErrBridgeDown:
		return "bridge_down"
	case ErrUpstreamDown:
		return "upstream_down"
	case ErrClient:
		return "client"
	default:
		return "none"
	}
}

// Error 带分类的上游错误。
type Error struct {
	Kind   ErrKind
	Status int
	Msg    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("upstream %s (http %d): %s", e.Kind, e.Status, e.Msg)
}

// BridgeDown 把「连不上本地桥」的 transport error 包装成可分类错误。
//
// 为什么必须区分：本地桥（zcode/copilot/doubao 等自建 127.0.0.1 服务）进程没在
// 跑时，请求会以 connection refused 失败。若照旧计进账号 errCount，桥一挂就会在
// 几秒内把整池账号各罚一次长冷却（2026-09-30 事故：5 个 zcode 号全被罚 10 分钟，
// 余额 85M~98M 明明健康），随后渠道熔断 0/5 healthy 反复 trip。桥的死活与账号
// 凭证/额度无关，故单列一类走短冷却。
func BridgeDown(err error) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: ErrBridgeDown, Msg: err.Error()}
}

// upstreamUnreachableMarkers 传输层「上游网络不可达」特征串（解析/连接/读写三层失败）。
//
// 云渠道（workbuddy/intl/codebuddy 等）由本机直连上游，网络抖动时 Go http 客户端在
// 传输层抛出 *url.Error，错误串含下列特征；zcode 桥透传的
// {"type":"upstream_unreachable",...} 响应体也复用同一份串。本地桥连不上的场景
// 由 BridgeDown 标记，与此处区分。
//
// 三层覆盖（均为本机/链路侧网络故障，与账号凭证无关）：
//   - 解析层：no such host / getaddrinfo / enotfound / eai_again（2026-10-05 workbuddy 事故）
//   - 连接层：dial tcp
//   - 读写层：wsarecv / wsasend + connection attempt failed / forcibly closed，
//     i/o timeout、connection reset、broken pipe、EOF（2026-10-09 codebuddy 事故：
//     read tcp ... wsarecv ... did not properly respond，TCP 已建连但对端经代理链路
//     不回数据，三次即攒满 errThreshold 触发整渠道 10 分钟冷却）
var upstreamUnreachableMarkers = []string{
	"upstream_unreachable",
	"getaddrinfo",
	"enotfound",
	"eai_again",
	"no such host",
	"dial tcp",
	"wsarecv",
	"wsasend",
	"connection attempt failed",
	"forcibly closed",
	"connection reset",
	"reset before headers",
	"connection timeout",
	"broken pipe",
	"i/o timeout",
	"eof",
}

// LooksUpstreamUnreachable 判断字符串（响应体或传输层错误串）是否为上游域名/网络
// 不可达。zcode 的 body 分类与本包的传输层错误分类共用同一份特征串，避免两处漂移。
func LooksUpstreamUnreachable(s string) bool {
	low := strings.ToLower(s)
	for _, m := range upstreamUnreachableMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// IsUpstreamUnreachable 判断传输层错误是否为上游域名/网络不可达（本机 DNS 抖动、
// 网络瞬断等）——与账号凭证/额度无关，绝不可计入账号 errCount。
//
// 为什么必须区分：云渠道直连时一次 23 秒的 DNS 抖动会让整池账号各吃 3 次错误 →
// 10 分钟长冷却 → 渠道熔断反复自触发（2026-10-05 事故：workbuddy 4 个号全冻 10 分钟，
// 而 DNS 本身只抖了 23 秒）。同 ErrUpstreamDown 规格，只给短冷却。
func IsUpstreamUnreachable(err error) bool {
	if err == nil {
		return false
	}
	return LooksUpstreamUnreachable(err.Error())
}

// IsLoopbackURL 判断上游地址是否指向本机（即自建本地桥，而非云端渠道）。
// 用 url/IP 解析而非子串匹配，避免把 127.0.0.1.nip.io、localhost.example.com
// 这类真实公网域名误判成本地桥。
func IsLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ModelInfo 动态/静态模型信息。
type ModelInfo struct {
	ID            string
	Name          string
	ContextWindow int64
	MaxTokens     int64
}

// ModelPricing 模型积分定价（从上游 API 拉取）。
type ModelPricing struct {
	Model   string  `json:"model"`
	Channel string  `json:"channel"`
	Rate    float64 `json:"rate"`
	Note    string  `json:"note,omitempty"` // 折扣/标签说明
}

// Upstream 是 server/scheduler 依赖的最小上游能力集合。
type Upstream interface {
	RefreshToken(a *auth.Auth) error
	ChatStream(a *auth.Auth, body []byte) (rc io.ReadCloser, status int, respBody []byte, err error)
	FetchModels(a *auth.Auth) ([]ModelInfo, error)
	FetchModelPricing(a *auth.Auth) ([]ModelPricing, error)
	UserResource(a *auth.Auth) (int64, error)
	UserResourceDetail(a *auth.Auth) (int64, []ResourceItem, error)
	DailyCheckin(a *auth.Auth) error
	GrowthTravel(a *auth.Auth) error
	GrowthLottery(a *auth.Auth) error
	GrowthTasksDetailed(a *auth.Auth) GrowthTasksResult
	Classify(status int, body string) ErrKind
	Stream(w http.ResponseWriter, r io.Reader) error
	Aggregate(r io.Reader) (map[string]any, error)
}

// ThinkPassthrough 可选能力：请求体带 "think": true 的客户端（chatui 等）
// 可索取思考流（reasoning_content）。实现该接口的上游在转发时保留思考字段；
// 未实现的渠道按默认行为处理（多数本就不剥离，如 qoder/traework）。
type ThinkPassthrough interface {
	StreamWithThink(w http.ResponseWriter, r io.Reader, passthrough bool) error
	AggregateWithThink(r io.Reader, keepReasoning bool) (map[string]any, error)
}

// ResourceItem 积分明细条目。
type ResourceItem struct {
	Name     string `json:"name"`
	Total    int64  `json:"total"`
	Used     int64  `json:"used"`
	Remain   int64  `json:"remain"`
	ExpireAt int64  `json:"expire_at,omitempty"` // 该套餐到期时间（Unix 秒）；0=长期有效
}

// EarliestExpire 返回 items 中最早到期的套餐时间；全部长期有效则返回 0。
// 已用尽的套餐（Remain<=0）不参与排序：其到期时间对路由/展示无意义，
// 只会让面板恒显示一个"最早但已用光"的过期时间（如 Trae 串行签到包）。
// 路由"快到期优先"依赖此值：0 视为长期（最后使用），>0 越早越优先。
func EarliestExpire(items []ResourceItem) int64 {
	var e int64
	for _, it := range items {
		if it.Remain <= 0 {
			continue
		}
		if it.ExpireAt > 0 && (e == 0 || it.ExpireAt < e) {
			e = it.ExpireAt
		}
	}
	return e
}

// GrowthTasksResult 成长中心任务领取结果。
type GrowthTasksResult struct {
	Message      string       `json:"message"`
	Rewards      []TaskReward `json:"rewards"`
	TotalCredits int64        `json:"total_credits"`
	CouponCount  int          `json:"coupon_count"`
	AuthErr      bool         `json:"auth_err,omitempty"`
}

// TaskReward 任务奖励详情。
type TaskReward struct {
	Name       string `json:"name"`
	Credits    int64  `json:"credits"`
	CouponName string `json:"coupon_name,omitempty"`
}
