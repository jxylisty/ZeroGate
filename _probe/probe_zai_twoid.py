# -*- coding: utf-8 -*-
"""确认 AutoClaw 代理双标识路由：X-Request-Model=完整路由ID + body.model=剥前缀模型ID."""
import json, uuid, http.client, ssl

AUTH = json.load(open(r"D:\wild-work\src-true\auths\autoclaw-1a09c829e67b3409869be85298ff257b.json", encoding="utf-8"))
tok = AUTH["auth"]["accessToken"].replace("Bearer ", "")

def probe(xreq, body_model, consent):
    body = json.dumps({"model": body_model, "messages": [{"role": "user", "content": "hi"}], "max_tokens": 4})
    h = {
        "Content-Type": "application/json", "Accept": "text/event-stream",
        "X-Authorization": "Bearer " + tok, "X-Request-Id": uuid.uuid4().hex,
        "X-Request-Model": xreq, "X-Client-Type": "pc", "X-Product": "autoclaw",
        "X-Harness-Type": "zcode", "X-Tm": "win32", "X-Lang": "en", "x_trace_id": "autoclaw-desktop",
    }
    conn = http.client.HTTPSConnection("autoglm-acceleration-api.zhipuai.cn", 443, timeout=30,
                                       context=ssl.create_default_context())
    try:
        conn.request("POST", "/autoclaw-proxy/proxy/autoclaw/chat/completions", body=body, headers=h)
        r = conn.getresponse(); raw = r.read(300)
        print(f"X-Request-Model={xreq!r:26} body.model={body_model!r:20} -> {r.status} {raw[:140].decode(errors='replace')}")
    except Exception as e:
        print(f"X-Request-Model={xreq!r:26} body.model={body_model!r:20} -> EXC {type(e).__name__}: {e}")
    finally:
        conn.close()

probe("zai_glm-5.3-flash", "glm-5.3-flash", "")      # flash 双标识（预期 200）
probe("zaicoding_glm-5.3", "glm-5.3", "")            # glm-5.3 双标识（预期 200）
probe("zai_auto", "auto", "")                        # auto 剥前缀形态
probe("zai_auto-fast", "auto-fast", "")              # auto-fast 剥前缀形态
print("done")