# -*- coding: utf-8 -*-
"""打印两号 Token_Rush/M9W 系 modal 的完整时间窗与元数据。"""
import json, time, hashlib, urllib.request, glob

SECRET = "38d2391985e2369a5fb8227d8e6cd5e5"
BASE = "https://autoglm-acceleration-api.zhipuai.cn"

def load_token(path):
    j = json.load(open(path, encoding="utf-8"))
    tok = (j.get("auth") or {}).get("accessToken") or j.get("accessToken") or ""
    return tok.replace("Bearer ", "")

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

for path in sorted(glob.glob(r"D:\wild-work\src-true\auths\autoclaw-*.json")):
    tok = load_token(path)
    req = urllib.request.Request(BASE + "/autoclaw-proxy/proxy/autoclaw-promotion-config", headers=headers(tok))
    cfg = json.loads(urllib.request.urlopen(req, timeout=20).read())["data"]
    print("=" * 15, path.split("\\")[-1], "=" * 15)
    for m in cfg.get("modals", []):
        mid = m.get("modal_id", "")
        if any(k in mid for k in ("M9W", "Token_Rush", "7M5", "0907")):
            print(f"id={mid} name={m.get('name')} btn=[{m.get('btnText')}] "
                  f"onlyOnce={m.get('onlyOnce')} intervalDays={m.get('intervalDays')} intervalHours={m.get('intervalHours')}")
            print(f"   start={m.get('startTime')} end={m.get('endTime')} tm={m.get('tm')} "
                  f"metadata={json.dumps(m.get('metadata'), ensure_ascii=False)}")
