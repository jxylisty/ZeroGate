// Package doubao 豆包桌面客户端会话保活续期。
// 协议移植自 smart-open/TraeWorkAssistant（MIT）src-tauri/src/tasks/doubao_session.rs：
// 凭证 = 豆包 User Data\Local State 内嵌的 x-tt-multi-sids 明文 sessionid（uid→sid 映射），
// 无额外设备凭证、无需逆向。
// 两段式探活（防「死会话被洗白」）：
//
//	① 已登录 JSON 端点权威判定（code=0 有效 / 710012001 失效）——info/v2/ 对任意 sid 恒 200，不可用于判定；
//	② 判定有效后才调保活端点，抓取 Set-Cookie（sessionid/sid_guard 滑动续期）回写。
package doubao

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	probeURL    = "https://www.doubao.com/alice/commerce/sale/subscription/quota/summary/"
	renewURL    = "https://www.doubao.com/info/v2/"
	expiredCode = 710012001
	userAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AIWorkAssistant/1.0"
)

// targetCookies 保活响应中需要回写的会话 cookie（对齐 TraeWorkAssistant）。
var targetCookies = []string{"sessionid", "sessionid_ss", "sid_tt", "uid_tt", "sid_guard"}

// Account 单个会话探活结果。
type Account struct {
	UserID      string `json:"user_id"`
	Status      string `json:"status"` // ok | expired | error
	Detail      string `json:"detail"`
	Renewed     bool   `json:"renewed"`
	SidExpireAt string `json:"sid_expire_at,omitempty"` // 由 sid_guard 换算的到期时间
}

// Result 一轮保活巡检结果。
type Result struct {
	FinishedAt string    `json:"finished_at"`
	OK         int       `json:"ok"`
	Expired    int       `json:"expired"`
	Error      int       `json:"error"`
	Accounts   []Account `json:"accounts"`
	Note       string    `json:"note,omitempty"`
}

// Renew 读取豆包 User Data 下的 Local State，对全部明文 sessionid 做两段式保活续期。
func Renew(localStatePath string) Result {
	res := Result{FinishedAt: time.Now().Format("01-02 15:04")}
	raw, err := os.ReadFile(localStatePath)
	if err != nil {
		res.Note = "未找到豆包 Local State（" + localStatePath + "）：" + short(err) + "；请确认豆包桌面客户端已安装并登录"
		return res
	}
	sids := readMultiSids(raw)
	if len(sids) == 0 {
		res.Note = "Local State 中未解析到会话（x-tt-multi-sids 为空）；请打开一次豆包客户端后重试"
		return res
	}
	client := newProbeClient()
	uids := make([]string, 0, len(sids))
	for uid := range sids {
		uids = append(uids, uid)
	}
	for _, uid := range uids {
		sid := sids[uid]
		acc := Account{UserID: uid}
		// ① 权威判定
		firstStatus, firstDetail := apiProbe(client, sid)
		switch firstStatus {
		case "expired":
			acc.Status, acc.Detail = "expired", firstDetail
			res.Expired++
		case "error":
			acc.Status, acc.Detail = "error", firstDetail
			res.Error++
		default: // ok
			// ② 保活请求抓续期 Cookie；保活请求网络失败不改状态（会话刚被权威端点判定有效）
			st, newCookies, dt := renewProbe(client, sid)
			if st == "error" {
				acc.Status, acc.Detail = "ok", "probe ok; renew: "+dt
				res.OK++
			} else {
				acc.Status, acc.Detail = st, dt
				if st == "expired" {
					res.Expired++
				} else {
					res.OK++
				}
				if newSid := newCookies["sessionid"]; newSid != "" {
					acc.Renewed = true
				}
				if guard := newCookies["sid_guard"]; guard != "" {
					acc.SidExpireAt = expireFromSidGuard(guard)
				}
			}
		}
		res.Accounts = append(res.Accounts, acc)
	}
	return res
}

// readMultiSids 解析 Local State JSON：saman.local_storage_app_for_web.enterprise 内嵌的
// x-tt-multi-sids（uid → 明文 sessionid，key 全部为数字、以 | 分隔）。对齐 Rust 实现。
func readMultiSids(raw []byte) map[string]string {
	out := map[string]string{}
	var ls struct {
		Saman struct {
			LSWeb struct {
				Enterprise string `json:"enterprise"`
			} `json:"local_storage_app_for_web"`
		} `json:"saman"`
	}
	if err := json.Unmarshal(raw, &ls); err != nil {
		return out
	}
	ent := ls.Saman.LSWeb.Enterprise
	i := strings.Index(ent, "x-tt-multi-sids")
	if i < 0 {
		return out
	}
	// 跳过键名(15) + `:"`(3) 共 18 字节，取至下一引号
	after := ent[i+18:]
	if end := strings.Index(after, `"`); end >= 0 {
		after = after[:end]
	}
	decoded, err := url.QueryUnescape(after)
	if err != nil {
		return out
	}
	for _, pair := range strings.Split(decoded, "|") {
		k, v, ok := strings.Cut(pair, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		allDigits := true
		for _, c := range k {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			out[k] = v
		}
	}
	return out
}

// newProbeClient 探活专用 client：15s 超时 + 不跟随重定向（30x 交由业务判定是否跳 passport）。
func newProbeClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func cookieHeader(sid string) string {
	return "sessionid=" + sid + "; sessionid_ss=" + sid
}

// apiProbe 已登录端点权威判定，返回 (status, detail)，status ∈ ok | expired | error。
func apiProbe(client *http.Client, sid string) (string, string) {
	req, _ := http.NewRequest(http.MethodPost, probeURL, bytes.NewReader([]byte(`{"product_line":"membership"}`)))
	req.Header.Set("Cookie", cookieHeader(sid))
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://www.doubao.com/")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "error", "请求失败: " + short(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var payload struct {
		Code json.Number `json:"code"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "error", "响应非 JSON"
	}
	if payload.Code.String() == "" { // 无 code / null → 视为有效（对齐 Rust）
		return "ok", "code=0"
	}
	code, _ := payload.Code.Int64()
	switch code {
	case 0:
		return "ok", "code=0"
	case expiredCode:
		return "expired", "code=710012001（登录态失效，会话已被服务端吊销或过期）"
	default:
		return "error", fmt.Sprintf("code=%d（业务拒绝）", code)
	}
}

// renewProbe 保活端点，返回 (status, newCookies, detail)，status ∈ ok | expired | error。
func renewProbe(client *http.Client, sid string) (string, map[string]string, string) {
	newCookies := map[string]string{}
	req, _ := http.NewRequest(http.MethodGet, renewURL, nil)
	req.Header.Set("Cookie", cookieHeader(sid))
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "error", newCookies, "请求失败: " + short(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		for _, sc := range resp.Header.Values("Set-Cookie") {
			kv, _, _ := strings.Cut(sc, ";")
			k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
			if ok && isTargetCookie(k) && v != "" {
				newCookies[k] = v
			}
		}
		return "ok", newCookies, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if resp.StatusCode >= 300 && resp.StatusCode < 400 && strings.Contains(strings.ToLower(loc), "passport") {
		return "expired", newCookies, fmt.Sprintf("302 → %s", truncate(loc, 120))
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return "expired", newCookies, "HTTP 401"
	}
	return "error", newCookies, fmt.Sprintf("HTTP %d %s", resp.StatusCode, truncate(loc, 80))
}

func isTargetCookie(name string) bool {
	for _, t := range targetCookies {
		if t == name {
			return true
		}
	}
	return false
}

// expireFromSidGuard sid_guard 格式 '<sid>|<create_ts 秒>|<duration 秒>|...' → 到期时间。
func expireFromSidGuard(guard string) string {
	g, err := url.QueryUnescape(guard)
	if err != nil {
		g = guard
	}
	parts := strings.Split(g, "|")
	if len(parts) < 3 {
		return ""
	}
	var create, dur int64
	fmt.Sscanf(parts[1], "%d", &create)
	fmt.Sscanf(parts[2], "%d", &dur)
	if create <= 0 || dur <= 0 {
		return ""
	}
	return time.Unix(create+dur, 0).Format("2006-01-02 15:04")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func short(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
