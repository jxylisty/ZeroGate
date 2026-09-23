# -*- coding: utf-8 -*-
"""列出两号当前时间窗内、按钮非"已领取"的活动 modal（潜在可领）。"""
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
        "authorization": f"Bearer {tok}", "X-Authorization": f"Bearer {tok}",
    }

now = int(time.time() * 1000)
for path in sorted(glob.glob(r"D:\wild-work\src-true\auths\autoclaw-*.json")):
    tok = load_token(path)
    req = urllib.request.Request(BASE + "/autoclaw-proxy/proxy/autoclaw-promotion-config", headers=headers(tok))
    cfg = json.loads(urllib.request.urlopen(req, timeout=20).read())["data"]
    print("=" * 15, path.split("\\")[-1][:22], "=" * 15)
    for m in cfg.get("modals", []):
        btn = m.get("btnText", "")
        if m.get("startTime", 0) <= now <= m.get("endTime", 0) and btn and "已领取" not in btn:
            print(f"ACTIVE: id={m.get('modal_id')} name={m.get('name')} btn=[{btn}] btnType={m.get('btnType')}")
    # 已领的活动也列出来做对照
    for m in cfg.get("modals", []):
        if "已领取" in m.get("btnText", "") and m.get("startTime", 0) <= now <= m.get("endTime", 0):
            print(f"  (claimed-in-window: {m.get('modal_id')})")
