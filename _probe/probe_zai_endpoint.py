# -*- coding: utf-8 -*-
"""验证 autoclaw accessToken 在 Z.ai 各候选 chat/completions 端点的可用性."""
import json, http.client, ssl

AUTH = json.load(open(r"D:\wild-work\src-true\auths\autoclaw-1a09c829e67b3409869be85298ff257b.json", encoding="utf-8"))
tok = AUTH["auth"]["accessToken"].replace("Bearer ", "")

candidates = [
    ("v1(当前→应404)", "https://api.z.ai/v1/chat/completions"),
    ("paas/v4 通用",     "https://api.z.ai/api/paas/v4/chat/completions"),
    ("coding/paas/v4 编码", "https://api.z.ai/api/coding/paas/v4/chat/completions"),
]

body = json.dumps({
    "model": "glm-5.3-flash",
    "messages": [{"role": "user", "content": "hi"}],
    "max_tokens": 8,
    "stream": False,
})

ctx = ssl.create_default_context()
for name, url in candidates:
    u = url.split("://", 1)[1]
    host, path = u.split("/", 1)
    path = "/" + path
    conn = http.client.HTTPSConnection(host, 443, timeout=30, context=ctx)
    try:
        headers = {
            "Content-Type": "application/json",
            "Authorization": "Bearer " + tok,
            "Accept": "application/json",
        }
        conn.request("POST", path, body=body, headers=headers)
        r = conn.getresponse()
        raw = r.read(600)
        print(f"### [{name}] {url}")
        print(f"    -> {r.status} {r.reason}")
        print("   ", raw[:400].decode(errors="replace"))
    except Exception as e:
        print(f"### [{name}] {url} -> EXC {type(e).__name__}: {e}")
    finally:
        conn.close()
print("done")