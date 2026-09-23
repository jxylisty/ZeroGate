# -*- coding: utf-8 -*-
"""拉 promotion-config 的 inConversation/inConversationList + recommendTask 完整结构。"""
import json, time, hashlib, urllib.request

SECRET = "38d2391985e2369a5fb8227d8e6cd5e5"
BASE = "https://autoglm-acceleration-api.zhipuai.cn"

def load_token(path):
    j = json.load(open(path, encoding="utf-8"))
    tok = (j.get("auth") or {}).get("accessToken") or j.get("accessToken") or ""
    return tok.replace("Bearer ", ""), (j.get("auth") or {}).get("deviceId") or j.get("deviceId") or ""

def headers(tok):
    ts = str(int(time.time()))
    sign = hashlib.md5(f"100003&{ts}&{SECRET}".encode()).hexdigest()
    return {
        "Content-Type": "application/json", "Accept": "*/*",
        "X-Version": "1.18.4", "X-Tm": "win", "X-Product": "autoclaw",
        "X-Auth-Appid": "100003", "X-Auth-TimeStamp": ts, "X-Auth-Sign": sign,
        "X-Trace-Id": "11111111-2222-3333-4444-555555555555",
        "X-Lang": "zh-CN", "X-Channel": "official",
        "authorization": f"Bearer {tok}",
    }

import glob
for path in sorted(glob.glob(r"D:\wild-work\src-true\auths\autoclaw-*.json")):
    tok, dev = load_token(path)
    req = urllib.request.Request(BASE + "/autoclaw-proxy/proxy/autoclaw-promotion-config",
                                 headers=headers(tok))
    raw = urllib.request.urlopen(req, timeout=20).read()
    cfg = json.loads(raw)["data"]
    print("=" * 20, path.split("\\")[-1], "=" * 20)
    for key in ("inConversation", "inConversationList", "recommendTask", "recommendTask_v2"):
        v = cfg.get(key)
        s = json.dumps(v, ensure_ascii=False)
        print(f"--- {key} (len={len(s)}) ---")
        print(s[:3000])
    print()
