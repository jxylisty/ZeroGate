// ZCode 领取（活动）的 captcha 载体页。
//
// 背景：`POST /zcode-plan/billing/claim` 始终强制索要阿里云 captcha param，
// 且该 param 只能由阿里云前端 SDK 的无感验证产出。项目里常驻实例侧用的是
// happy-dom「假 DOM」求解器，产出的 param 常被风控判为风险（`verify param
// too short` / 3007），导致每日/周末领取失败。
//
// 本文件只解决「param 从哪来」：用**本地载体页 + 用户真实浏览器**——
//
//	① 领取前打开 http://127.0.0.1:<port>/api/zcode/carrier?token=<一次性 id>
//	② 页面加载阿里云 SDK 完成无感验证，把 param POST 回 /carrier/contribute
//	③ 领取流程把 param 经环境变量注入给 zcode-proxy claim CLI
//
// 页面里**不含任何凭据**（region/prefix/sceneId 是阿里云的公开初始化参数）。
// 参考实现：tinylab internal/jethub/zcode_captcha.go（协议同源）。
package zcode

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"zerogate/internal/platform"
)

// CaptchaCarrierConfig 载体页初始化所需的三元组（阿里云公开参数，非凭据）。
type CaptchaCarrierConfig struct {
	Region  string
	Prefix  string
	SceneID string
}

// CaptchaParam 一次无感验证的产物（param + 对应 region）。
type CaptchaParam struct {
	Param  string
	Region string
}

// captchaCarrierFallback 兜底三元组：与线上 GET /api/v1/client/configs 实测
// 值一致；服务端换 sceneId/灰度时由 live 抓取覆盖，抓取失败也不阻塞领取。
var captchaCarrierFallback = CaptchaCarrierConfig{Region: "cn", Prefix: "no8xfe", SceneID: "11xygtvd"}

const (
	captchaCarrierConfigsAPI = "https://zcode.z.ai/api/v1/client/configs"
	captchaCarrierAppVersion = "3.14.3"
	captchaCarrierPlatform   = "win32-x64"

	// captchaMinParamLen / captchaMinTokenLen：长度 < 200 或 securityToken < 50
	// 的 param 必得 3007，宁可不发。
	captchaMinParamLen = 200
	captchaMinTokenLen = 50

	captchaWaitTimeout    = 90 * time.Second
	captchaSessionTTL     = 3 * time.Minute
	captchaConfigCacheTTL = time.Minute
)

// ValidateCaptchaParam 校验 param：长度 ≥ 200、base64 可解、JSON 含非空
// certifyId 且 securityToken ≥ 50（照抄参考实现的 validateCaptchaParam）。
func ValidateCaptchaParam(param string) error {
	if len(param) < captchaMinParamLen {
		return fmt.Errorf("param 过短（%d < %d）", len(param), captchaMinParamLen)
	}
	decoded, err := base64.StdEncoding.DecodeString(param)
	if err != nil {
		// Node 的 Buffer.from(x, 'base64') 容忍缺填充；再试无填充形态。
		decoded, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(param, "="))
		if err != nil {
			return fmt.Errorf("param 不是合法 base64：%w", err)
		}
	}
	var parsed struct {
		CertifyID     string `json:"certifyId"`
		SecurityToken string `json:"securityToken"`
	}
	if err := json.Unmarshal(decoded, &parsed); err != nil {
		return fmt.Errorf("param 解出的不是 JSON：%w", err)
	}
	if strings.TrimSpace(parsed.CertifyID) == "" {
		return fmt.Errorf("param 缺 certifyId")
	}
	if len(parsed.SecurityToken) < captchaMinTokenLen {
		return fmt.Errorf("param 的 securityToken 过短（%d < %d）", len(parsed.SecurityToken), captchaMinTokenLen)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 一次性载体会话（token → chan）
// ---------------------------------------------------------------------------

type carrierSession struct {
	ch      chan string
	expires time.Time
}

var carrierSessions = struct {
	sync.Mutex
	m map[string]*carrierSession
}{m: map[string]*carrierSession{}}

// registerCarrierSession 创建一个待回传会话并返回其一次性 token。
func registerCarrierSession(ttl time.Duration) (string, *carrierSession) {
	token := newCarrierToken()
	sess := &carrierSession{ch: make(chan string, 1), expires: time.Now().Add(ttl)}
	carrierSessions.Lock()
	defer carrierSessions.Unlock()
	// 进程内小表，顺手清理过期项，无需后台任务。
	now := time.Now()
	for key, entry := range carrierSessions.m {
		if entry.expires.Before(now) {
			delete(carrierSessions.m, key)
		}
	}
	carrierSessions.m[token] = sess
	return token, sess
}

// NewCarrierSession 注册一个待回传会话，返回一次性 token。
func NewCarrierSession() string {
	token, _ := registerCarrierSession(captchaSessionTTL)
	return token
}

// peekCarrierSession 读取会话但不消费（页面用它判断是否需要重试）。
func peekCarrierSession(token string) (*carrierSession, bool) {
	carrierSessions.Lock()
	defer carrierSessions.Unlock()
	sess, ok := carrierSessions.m[token]
	if !ok {
		return nil, false
	}
	if sess.expires.Before(time.Now()) {
		delete(carrierSessions.m, token)
		return nil, false
	}
	return sess, true
}

// consumeCarrierSession 取出并删除会话（一次性）。
func consumeCarrierSession(token string) (*carrierSession, bool) {
	if _, ok := peekCarrierSession(token); !ok {
		return nil, false
	}
	carrierSessions.Lock()
	sess := carrierSessions.m[token]
	delete(carrierSessions.m, token)
	carrierSessions.Unlock()
	return sess, true
}

// releaseCarrierSession 丢弃会话（不投递，如 mint 被取消/超时）。
func releaseCarrierSession(token string) {
	carrierSessions.Lock()
	defer carrierSessions.Unlock()
	delete(carrierSessions.m, token)
}

// CarrierPending 报告 token 是否仍在等待 param。
func CarrierPending(token string) bool {
	_, ok := peekCarrierSession(token)
	return ok
}

// DeliverCaptchaParam 由公开的 contribute 端点调用：校验 param 并投递给等待中的领取流程。
func DeliverCaptchaParam(token, param string) error {
	if err := ValidateCaptchaParam(param); err != nil {
		return err
	}
	// 校验通过才消费会话（非法 param 不消耗一次性 token，页面可重试）。
	sess, ok := consumeCarrierSession(token)
	if !ok {
		return fmt.Errorf("载体会话不存在或已过期")
	}
	select {
	case sess.ch <- param:
	default:
		return fmt.Errorf("该载体会话已收到 param")
	}
	return nil
}

// ---------------------------------------------------------------------------
// 载体页 HTML
// ---------------------------------------------------------------------------

// CarrierPageHTML 渲染一次性载体页。纯函数（页面自身的 Date.now() 运行时求值）。
func CarrierPageHTML(cfg CaptchaCarrierConfig, contributeURL, token string) string {
	configJSON, _ := json.Marshal(map[string]any{"region": cfg.Region, "prefix": cfg.Prefix})
	contributeJSON, _ := json.Marshal(contributeURL + "?token=" + token)
	sceneJSON, _ := json.Marshal(cfg.SceneID)
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<title>ZCode 验证</title>
<style>
 body{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;margin:2rem;color:#222}
 #zcode-aliyun-captcha-container{display:none}
 #status{font-size:15px;line-height:1.6}
 .ok{color:#0a7f2e}.err{color:#b00020}
</style></head><body>
<div id="status">正在完成阿里云无感验证…（通常 1 秒内完成，无需操作；若弹出滑块请拖动完成）</div>
<div id="zcode-aliyun-captcha-container" aria-hidden="true"><div id="zcode-aliyun-captcha-element"></div><button id="zcode-aliyun-captcha-button">verify</button></div>
<script>
(function () {
  var statusEl = document.getElementById('status');
  var contributeUrl = ` + string(contributeJSON) + `;
  var done = false;
  function finish(ok, text) {
    if (done) return;
    done = true;
    statusEl.className = ok ? 'ok' : 'err';
    statusEl.textContent = text;
  }
  function post(param) {
    return fetch(contributeUrl, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ param: param })
    }).then(function (r) {
      if (!r.ok) throw new Error('HTTP ' + r.status);
      finish(true, '验证完成，正在关闭本页…');
      closeSelf();
    }).catch(function (e) {
      finish(false, '回传失败：' + e + '（请刷新本页重试）');
    });
  }
  // 完成后尝试静默关闭本页，避免标签页堆积。浏览器对「非脚本打开」的标签页
  // 可能拒绝 close()，此时退化为极简成功态（一眼可认、便于手动关闭）。
  function closeSelf() {
    try { window.close(); } catch (e) { /* 忽略：被拒时走下方兜底 */ }
    setTimeout(function () {
      document.title = '✓ 已完成';
      document.body.innerHTML = '<div id="status" class="ok">✓ 已完成，可关闭本页</div>';
    }, 400);
  }
  var script = document.createElement('script');
  script.src = 'https://o.alicdn.com/captcha-frontend/aliyunCaptcha/AliyunCaptcha.js';
  script.async = true;
  script.onload = function () { start(); };
  script.onerror = function () { finish(false, '阿里云验证脚本加载失败（检查网络后刷新）'); };
  document.head.appendChild(script);
  setTimeout(function () { finish(false, '验证超时（请刷新本页重试）'); }, 60000);

  function start() {
    try {
      window.AliyunCaptchaConfig = ` + string(configJSON) + `;
      initAliyunCaptcha({
        SceneId: ` + string(sceneJSON) + `,
        mode: 'popup',
        element: '#zcode-aliyun-captcha-element',
        button: document.getElementById('zcode-aliyun-captcha-button'),
        getInstance: function (instance) {
          try {
            if (instance && typeof instance.startTracelessVerification === 'function') {
              instance.startTracelessVerification();
            } else if (instance && typeof instance.show === 'function') {
              instance.show();
            }
          } catch (e) {
            finish(false, '触发验证失败：' + e);
          }
        },
        success: function (param) { post(param); },
        fail: function (e) { finish(false, '验证失败：' + e); },
        onError: function (e) { finish(false, '验证出错：' + e); }
      });
    } catch (e) {
      finish(false, '初始化失败：' + e);
    }
  }
})();
</script></body></html>`
}

// CarrierPage 渲染 token 对应的载体页；ok=false 表示 token 未知/过期（端点回 404）。
func CarrierPage(token, contributeURL string) (string, bool) {
	if !CarrierPending(token) {
		return "", false
	}
	return CarrierPageHTML(carrierConfigSnapshot(), contributeURL, token), true
}

// ---------------------------------------------------------------------------
// captcha 配置抓取（60s 缓存 + 兜底）
// ---------------------------------------------------------------------------

var carrierConfigCache = struct {
	sync.Mutex
	cfg     CaptchaCarrierConfig
	expires time.Time
	loaded  bool
}{}

// carrierConfigSnapshot 返回 captcha 初始化参数，优先 live 抓取（5s 预算），
// 失败退回内置三元组——配置抓取失败绝不能阻塞领取。
func carrierConfigSnapshot() CaptchaCarrierConfig {
	carrierConfigCache.Lock()
	if carrierConfigCache.loaded && time.Now().Before(carrierConfigCache.expires) {
		cfg := carrierConfigCache.cfg
		carrierConfigCache.Unlock()
		return cfg
	}
	carrierConfigCache.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	cfg := fetchCarrierConfig(ctx)
	cancel()

	carrierConfigCache.Lock()
	carrierConfigCache.cfg = cfg
	carrierConfigCache.expires = time.Now().Add(captchaConfigCacheTTL)
	carrierConfigCache.loaded = true
	carrierConfigCache.Unlock()
	return cfg
}

// fetchCarrierConfig 抓取上游 captcha 配置；任何失败都退回兜底三元组。
func fetchCarrierConfig(ctx context.Context) CaptchaCarrierConfig {
	url := fmt.Sprintf("%s?app_version=%s&platform=%s", captchaCarrierConfigsAPI, captchaCarrierAppVersion, captchaCarrierPlatform)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return captchaCarrierFallback
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return captchaCarrierFallback
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return captchaCarrierFallback
	}
	var parsed struct {
		Data struct {
			Configs struct {
				Captcha struct {
					Enabled bool   `json:"enabled"`
					Region  string `json:"region"`
					Prefix  string `json:"prefix"`
					SceneID string `json:"sceneId"`
				} `json:"captcha"`
			} `json:"configs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return captchaCarrierFallback
	}
	c := parsed.Data.Configs.Captcha
	if !c.Enabled || c.Region == "" || c.Prefix == "" || c.SceneID == "" {
		return captchaCarrierFallback
	}
	return CaptchaCarrierConfig{Region: c.Region, Prefix: c.Prefix, SceneID: c.SceneID}
}

// ---------------------------------------------------------------------------
// mint：开载体页 → 等 param
// ---------------------------------------------------------------------------

// MintCaptchaParam 打开载体页（用户真实浏览器）并等待回传的 param。
// baseURL 形如 http://127.0.0.1:7863（无尾斜杠）。
func MintCaptchaParam(ctx context.Context, baseURL string) (CaptchaParam, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return CaptchaParam{}, fmt.Errorf("无法构造载体页地址（carrierBase 未配置）")
	}
	token, sess := registerCarrierSession(captchaSessionTTL)
	pageURL := baseURL + "/api/zcode/carrier?token=" + token
	if err := platform.OpenURL(pageURL); err != nil {
		releaseCarrierSession(token)
		return CaptchaParam{}, fmt.Errorf("打开载体页失败：%w", err)
	}
	cfg := carrierConfigSnapshot()
	select {
	case param := <-sess.ch:
		return CaptchaParam{Param: param, Region: cfg.Region}, nil
	case <-ctx.Done():
		releaseCarrierSession(token)
		return CaptchaParam{}, fmt.Errorf("领取已取消")
	case <-time.After(captchaWaitTimeout):
		releaseCarrierSession(token)
		return CaptchaParam{}, fmt.Errorf("等待验证超时（未在 %.0f 秒内收到 param）——请确认浏览器已打开载体页并完成验证",
			captchaWaitTimeout.Seconds())
	}
}

// newCarrierToken 生成 16 字节随机 hex 一次性 token。
func newCarrierToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败属极端异常；回退到时间戳，token 仅为进程内索引。
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
