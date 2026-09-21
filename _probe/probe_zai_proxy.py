# -*- coding: utf-8 -*-
"""验证 autoclaw 桌面代理端点如何为 glm-5.3-flash 提供模型，定位 404/非法模型 真实根因."""
import json, uuid, http.client, ssl

AUTH = json.load(open(r"D:\wild-work\src-true\auths\autoclaw-1a09c829e67b3409869be85298ff257b.json", encoding="utf-8"))
tok = AUTH["auth"]["accessToken"].replace("Bearer ", "")

def probe(host, path, headers, body, name):
    conn = http.client.HTTPSConnection(host, 443, timeout=30, context=ssl.create_default_context())
    try:
        conn.request("POST", path, body=body, headers=headers)
        r = conn.getresponse()
        raw = r.read(700)
        print(f"### [{name}] {host}{path}")
        print(f"    -> {r.status} {r.reason}")
        print("   ", raw[:500].decode(errors="replace"))
    except Exception as e:
        print(f"### [{name}] {host}{path} -> EXC {type(e).__name__}: {e}")
    finally:
        conn.close()

body = json.dumps({
    "model": "glm-5.3-flash",
    "messages": [{"role": "user", "content": "hi"}],
    "max_tokens": 8,
})
PROXY = "autoglm-acceleration-api.zhipuai.cn"
PX = "/autoclaw-proxy/proxy/autoclaw/chat/completions"
desktop = {
    "Content-Type": "application/json",
    "Accept": "text/event-stream",
    "X-Authorization": "Bearer " + tok,
    "X-Request-Id": uuid.uuid4().hex,
    "X-Request-Model": "glm-5.3-flash",
    "X-Client-Type": "pc",
    "X-Product": "autoclaw",
    "X-Harness-Type": "zcode",
    "X-Tm": "win32",
    "X-Lang": "en",
    "x_trace_id": "autoclaw-desktop",
}

probe(PROXY, PX, desktop, body, "代理 + glm-5.3-flash(桌面指纹)")
# 对比:代理 + 已知可用的 zai_auto
body2 = json.dumps({"model": "zai_auto", "messages": [{"role": "user", "content": "hi"}], "max_tokens": 8})
h2 = dict(desktop); h2["X-Request-Model"] = "zai_auto"
probe(PROXY, PX, h2, body2, "代理 + zai_auto(桌面指纹→对照)")
print("done")