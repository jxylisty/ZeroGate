# -*- coding: utf-8 -*-
"""枚举 AutoClaw 代理端点支持的 model id，确定各模型在代理侧的真实可用 id."""
import json, uuid, http.client, ssl

AUTH = json.load(open(r"D:\wild-work\src-true\auths\autoclaw-1a09c829e67b3409869be85298ff257b.json", encoding="utf-8"))
tok = AUTH["auth"]["accessToken"].replace("Bearer ", "")

for mid in ["zai_auto", "zai_auto-fast", "zaicoding_glm-5.3", "glm-5.3", "glm-5.3-flash", "flash"]:
    body = json.dumps({"model": mid, "messages": [{"role": "user", "content": "hi"}], "max_tokens": 4})
    headers = {
        "Content-Type": "application/json",
        "Accept": "text/event-stream",
        "X-Authorization": "Bearer " + tok,
        "X-Request-Id": uuid.uuid4().hex,
        "X-Request-Model": mid,
        "X-Client-Type": "pc",
        "X-Product": "autoclaw",
        "X-Harness-Type": "zcode",
        "X-Tm": "win32",
        "X-Lang": "en",
        "x_trace_id": "autoclaw-desktop",
    }
    conn = http.client.HTTPSConnection("autoglm-acceleration-api.zhipuai.cn", 443, timeout=30,
                                       context=ssl.create_default_context())
    try:
        conn.request("POST", "/autoclaw-proxy/proxy/autoclaw/chat/completions", body=body, headers=headers)
        r = conn.getresponse()
        raw = r.read(500)
        served = ""
        try:
            j = json.loads(raw)
            served = 'served_model=' + j.get("model", "?")
        except Exception:
            pass
        print(f"model={mid!r:22} -> {r.status} {r.reason}  {served}  {raw[:160].decode(errors='replace')}")
    except Exception as e:
        print(f"model={mid!r:22} -> EXC {type(e).__name__}: {e}")
    finally:
        conn.close()
print("done")