# -*- coding: utf-8 -*-
"""5424 小号：查 Token_Rush 当前状态 → API 领取 → 验证到账（积分钱包 + token 钱包两口径）。"""
import json, time, hashlib, urllib.request, glob

SECRET = "38d2391985e2369a5fb8227d8e6cd5e5"
BASE = "https://autoglm-acceleration-api.zhipuai.cn"

def load(path):
    j = json.load(open(path, encoding="utf-8"))
    tok = ((j.get("auth") or {}).get("accessToken") or j.get("accessToken") or "").replace("Bearer ", "")
    dev = (j.get("auth") or {}).get("deviceId") or j.get("deviceId") or ""
    return tok, dev

def hdr(tok):
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

def get(url, tok):
    req = urllib.request.Request(url, headers=hdr(tok))
    return json.loads(urllib.request.urlopen(req, timeout=20).read())

def post(payload, tok):
    req = urllib.request.Request(BASE + "/autoclaw-proxy/proxy/autoclaw-promotion-reward",
                                 data=json.dumps(payload).encode(), headers=hdr(tok), method="POST")
    try:
        r = urllib.request.urlopen(req, timeout=20)
        return r.status, r.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", errors="replace")

def token_wallet(tok):
    """token 口径钱包（wallet-instances 全 scope，抓 token 类实例余额）。"""
    try:
        d = get(BASE + "/agent-assetmgr/api/v1/wallet-instances?wallet_type=all&wallet_scope=all", tok)
        out = []
        for it in (d.get("data") or []):
            name = it.get("display_name") or it.get("wallet_type") or it.get("instance_id", "?")
            bal = it.get("balance", it.get("total_balance", "?"))
            wid = (it.get("wallet_type") or "")
            if "token" in str(wid).lower() or "token" in str(name).lower():
                out.append(f"{name}({wid})={bal}")
        return out
    except Exception as e:
        return [f"ERR {e}"]

paths = sorted(glob.glob(r"D:\wild-work\src-true\auths\autoclaw-*.json"))
# 5424 是第二个文件
tok, dev = None, None
for p in paths:
    if "54243632" in p:
        tok, dev = load(p)
        break
assert tok, "5424 auth file not found"

print("== 1. Token_Rush 当前状态 ==")
cfg = get(BASE + "/autoclaw-proxy/proxy/autoclaw-promotion-config", tok)["data"]
now = int(time.time() * 1000)
for m in cfg.get("modals", []):
    if "Token_Rush" in m.get("modal_id", "") and m.get("startTime", 0) <= now <= m.get("endTime", 0):
        print(f"  id={m['modal_id']} btn=[{m.get('btnText')}] window={m.get('startTime')}~{m.get('endTime')} intervalDays={m.get('intervalDays')}")

print("== 2. 领取前钱包 ==")
w = get(BASE + "/agent-assetmgr/api/v2/wallets?biz_app_id=autoclaw", tok)["data"]
print("  points total =", w["total_balance"])
print("  token wallets:", token_wallet(tok) or "(无 token 类实例或读取失败)")

print("== 3. 发起领取（双头契约）==")
for mid in ("Token_Rush_M9W4_2", "Token_Rush_M9W4"):
    for rt in ("sendReward", "timingReward"):
        s, b = post({"source_id": "autoclaw", "device_id": dev, "modal_id": mid, "reward_type": rt}, tok)
        print(f"  {mid} {rt}: HTTP {s} {b[:200]}")

time.sleep(3)
print("== 4. 领取后钱包 ==")
w2 = get(BASE + "/agent-assetmgr/api/v2/wallets?biz_app_id=autoclaw", tok)["data"]
print("  points total =", w2["total_balance"], f"(delta={w2['total_balance']-w['total_balance']:+d})")
print("  token wallets:", token_wallet(tok) or "(无 token 类实例或读取失败)")
cfg2 = get(BASE + "/autoclaw-proxy/proxy/autoclaw-promotion-config", tok)["data"]
for m in cfg2.get("modals", []):
    if "Token_Rush" in m.get("modal_id", "") and m.get("startTime", 0) <= now <= m.get("endTime", 0):
        print(f"  after: id={m['modal_id']} btn=[{m.get('btnText')}]")
