// Package provider 定义不同上游（workbuddy / traework）共用的最小接口。
// 只抽取 server/scheduler 必需能力，避免为未来平台过度设计。
package provider

import (
	"fmt"
	"io"
	"net/http"

	"zerogate/internal/auth"
)

// Kind 平台标识，同时也是模型名前缀。
type Kind string

const (
	WorkBuddy Kind = "workbuddy"
	TraeWork  Kind = "traework"
	Qoder     Kind = "qoder"
	CodeBuddy Kind = "codebuddy"  // 国际版 www.codebuddy.ai
	WBIntl    Kind = "wbintl"     // WorkBuddy 国际版 www.workbuddy.ai（同后端，独立入口）
	ZCode     Kind = "zcode"      // 智谱 ZCode (Z.AI) https://zcode.z.ai
	AutoClaw  Kind = "autoclaw"   // AutoClaw（Z.ai 官方代理通道）
	Atria     Kind = "atria"      // Atria Dawn Preview（上海人工智能实验室）https://api.atria-asi.ai
	Copilot   Kind = "copilot"    // 本机白嫖服务 127.0.0.1:4141（OpenAI 兼容）
	Doubao    Kind = "doubao"     // 本机白嫖服务 127.0.0.1:7868（OpenAI 兼容）
	GptGemini Kind = "gptgemini"  // 本机白嫖服务 127.0.0.1:8317（OpenAI 兼容）
	GlmFlash  Kind = "glmflash"   // 智谱开放平台 Flash 家族（永久免费层，OpenAI 兼容）
	Groq      Kind = "groq"       // Groq 免费层（Llama/Qwen 推理云，OpenAI 兼容）
	Tierflow  Kind = "tierflow"   // tierflow.cn 免费聚合（OpenAI 兼容，Bearer Key）
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
	Message      string        `json:"message"`
	Rewards      []TaskReward  `json:"rewards"`
	TotalCredits int64         `json:"total_credits"`
	CouponCount  int           `json:"coupon_count"`
	AuthErr      bool          `json:"auth_err,omitempty"`
}

// TaskReward 任务奖励详情。
type TaskReward struct {
	Name       string `json:"name"`
	Credits    int64  `json:"credits"`
	CouponName string `json:"coupon_name,omitempty"`
}
