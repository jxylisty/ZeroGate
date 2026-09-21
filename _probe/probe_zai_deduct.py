# -*- coding: utf-8 -*-
"""实测 glm-5.3-flash 双标识调用是否真的扣 AutoClaw 积分：取钱包余额→发真实请求→再取余额对比."""
import json, uuid, hashlib, time, http.client, ssl

SECRET = "38d2391985e2369a5fb8227d8e6cd5e5"
APPID = "100003"
AUTH = json.load(open(r"D:\wild-work\src-true\auths\autoclaw-1a09c829e67b3409869be85298ff257b.json", encoding="utf-8"))
tok = AUTH["auth"]["accessToken"].replace("Bearer ", "")
ctx = ssl.create_default_context()

def userapi_headers():
    ts = str(int(time.time()))
    sign = hashlib.md5(f"{APPID}&{ts}&{SECRET}".encode()).hexdigest()
    return {
        "Content-Type": "application/json", "Accept": "*/*", "X-Version": "1.12.1",
        "X-Tm": "web", "X-Product": "autoclaw", "X-Client-Type": "web",
        "X-Channel": "official", "X-Auth-Appid": APPID, "X-Auth-TimeStamp": ts,
        "X-Auth-Sign": sign, "X-Trace-Id": uuid.uuid4().hex, "X-Lang": "en",
        "Authorization": "Bearer " + tok,
    }

def get_balance(tag):
    conn = http.client.HTTPSConnection("autoglm-acceleration-api.zhipuai.cn", 443, timeout=30, context=ctx)
    try:
        conn.request("GET", "/agent-assetmgr/api/v2/wallets?biz_app_id=autoclaw", headers=userapi_headers())
        r = conn.getresponse(); raw = r.read()
        print(f"[{tag}] GET wallets -> {r.status} {raw.decode(errors='replace')[:800]}")
        d = None
        try:
            d = json.loads(raw)
        except Exception:
            pass
        return d
    finally:
        conn.close()

def request_chat():
    body = json.dumps({
        "model": "glm-5.3-flash",
        "messages": [{"role": "user", "content": "请用中文写一段约150字的关于早春景色的短文。"}],
        "stream": False,
    })
    h = {
        "Content-Type": "application/json", "Accept": "application/json",
        "X-Authorization": "Bearer " + tok, "X-Request-Id": uuid.uuid4().hex,
        "X-Request-Model": "zai_glm-5.3-flash",   # 完整路由ID（会员/按量通道）
        "X-Client-Type": "pc", "X-Product": "autoclaw",
        "X-Harness-Type": "zcode", "X-Tm": "win32", "X-Lang": "en",
        "x_trace_id": "autoclaw-desktop",
    }
    conn = http.client.HTTPSConnection("autoglm-acceleration-api.zhipuai.cn", 443, timeout=120, context=ctx)
    try:
        conn.request("POST", "/autoclaw-proxy/proxy/autoclaw/chat/completions", body=body, headers=h)
        r = conn.getresponse(); raw = r.read(4000)
        print(f"[chat] -> {r.status} {raw[:1200].decode(errors='replace')}")
        return r.status
    finally:
        conn.close()

print("== 发起前余额 =="); get_balance("before")
st = request_chat()
print("等待积分入账 6 秒..."); time.sleep(6)
print("== 发起后余额 =="); get_balance("after")
print("done")