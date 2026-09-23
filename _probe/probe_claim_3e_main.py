# -*- coding: utf-8 -*-
"""小号 1a09：Token_Rush 3亿 tokens 领取尝试。
积分钱包 + token 钱包（原始结构先摸清）+ 账单(ledgers) 三口径，领前领后对比。"""
import json, time, hashlib, urllib.request, glob

SECRET = "38d2391985e2369a5fb8227d8e6cd5e5"
BASE = "https://autoglm-acceleration-api.zhipuai.cn"
UID_PREFIX = "1a09c829"  # 小号

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

def get_raw(url, tok):
    req = urllib.request.Request(url, headers=hdr(tok))
    return json.loads(urllib.request.urlopen(req, timeout=25).read())

def post_reward(payload, tok):
    req = urllib.request.Request(BASE + "/autoclaw-proxy/proxy/autoclaw-promotion-reward",
                                 data=json.dumps(payload).encode(), headers=hdr(tok), method="POST")
    try:
        r = urllib.request.urlopen(req, timeout=20)
        return r.status, r.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", errors="replace")

tok = dev = None
for p in sorted(glob.glob(r"D:\wild-work\src-true\auths\autoclaw-*.json")):
    if UID_PREFIX in p:
        tok, dev = load(p)
        break
assert tok, "小号 auth 文件未找到"

print("== 0. wallet-instances 原始结构（先摸清字段）==")
raw = get_raw(BASE + "/agent-assetmgr/api/v1/wallet-instances?wallet_type=all&wallet_scope=all", tok)
s = json.dumps(raw, ensure_ascii=False)
print("  top keys:", list(raw.keys()))
print("  data type:", type(raw.get("data")).__name__)
print("  data[:1200]:", s[:1200])

print()
print("== 1. 领取前快照 ==")
pts_before = get_raw(BASE + "/agent-assetmgr/api/v2/wallets?biz_app_id=autoclaw", tok)["data"]["total_balance"]
print("  points total =", pts_before)

def snap_tokens():
    """token 类钱包快照（尽力解析，兼容 dict/list 两种 data）。"""
    raw = get_raw(BASE + "/agent-assetmgr/api/v1/wallet-instances?wallet_type=all&wallet_scope=all", tok)
    data = raw.get("data")
    items = []
    if isinstance(data, list):
        items = data
    elif isinstance(data, dict):
        for k in ("instances", "wallet_instances", "list", "items", "records"):
            if isinstance(data.get(k), list):
                items = data[k]
                break
        if not items:
            return {"raw_keys": list(data.keys())}
    out = {}
    for it in items:
        if not isinstance(it, dict):
            continue
        name = str(it.get("display_name") or it.get("name") or it.get("wallet_type") or it.get("asset_type") or "?")
        wid = str(it.get("wallet_type") or it.get("asset_type") or "")
        bal = it.get("balance", it.get("total_balance", it.get("remaining", "?")))
        out[f"{name}|{wid}"] = bal
    return out

before_tokens = snap_tokens()
print("  token wallets before =", json.dumps(before_tokens, ensure_ascii=False)[:800])

print()
print("== 2. 对 Token_Rush_M9W4_2 发起领取（双头契约）==")
for rt in ("sendReward", "timingReward"):
    s_, b = post_reward({"source_id": "autoclaw", "device_id": dev,
                         "modal_id": "Token_Rush_M9W4_2", "reward_type": rt}, tok)
    print(f"  sendReward→{rt}: HTTP {s_} {b[:220]}")

time.sleep(3)
print()
print("== 3. 领取后快照 ==")
pts_after = get_raw(BASE + "/agent-assetmgr/api/v2/wallets?biz_app_id=autoclaw", tok)["data"]["total_balance"]
print(f"  points total = {pts_after} (delta={pts_after - pts_before:+d})")
after_tokens = snap_tokens()
print("  token wallets after =", json.dumps(after_tokens, ensure_ascii=False)[:800])

print()
print("== 4. 今日账单（ledgers_std，权威入账记录）==")
try:
    led = get_raw(BASE + "/agent-assetmgr/api/v1/ledgers_std?asset_type=point&wallet_type=all&cursor=&page_size=10", tok)
    print(json.dumps(led.get("data"), ensure_ascii=False)[:1500])
except Exception as e:
    print("  ledgers ERR:", e)
