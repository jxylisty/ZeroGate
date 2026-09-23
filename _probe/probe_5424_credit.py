# -*- coding: utf-8 -*-
"""大号 5424：找 00:24 那笔 +2.8万 入账的账单元数据（source/description），反推领取机制。"""
import json, time, hashlib, urllib.request, glob

SECRET = "38d2391985e2369a5fb8227d8e6cd5e5"
BASE = "https://autoglm-acceleration-api.zhipuai.cn"

def load(path):
    j = json.load(open(path, encoding="utf-8"))
    return ((j.get("auth") or {}).get("accessToken") or j.get("accessToken") or "").replace("Bearer ", "")

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
    return json.loads(urllib.request.urlopen(req, timeout=25).read())

tok = None
for p in sorted(glob.glob(r"D:\wild-work\src-true\auths\autoclaw-*.json")):
    if "54243632" in p:
        tok = load(p)
        break

# 拉 50 条账单，找今天 00:20~00:40 的 credit(+)
led = get(BASE + "/agent-assetmgr/api/v1/ledgers_std?asset_type=point&wallet_type=all&cursor=&page_size=50&flow_direction=income", tok)
entries = (led.get("data") or {}).get("entries", [])
print(f"共拉到 {len(entries)} 条 income 账单：")
for e2 in entries:
    amt = e2.get("amount", 0)
    occurred = e2.get("occurred_at", "")
    if isinstance(amt, str):
        try: amt = float(amt)
        except: amt = 0
    if amt and amt > 0:
        md = e2.get("metadata") or {}
        print(f"--- +{amt} @ {occurred}")
        print(f"    desc={e2.get('description')}")
        print(f"    source={e2.get('source')} mutation={e2.get('mutation_type')} wallet={e2.get('wallet_type')}/{e2.get('public_wallet_type')}")
        interesting = {k: v for k, v in md.items() if k in (
            "reason", "activity_id", "modal_id", "reward_type", "source_id", "task_id",
            "cycle_key", "grant_type", "trigger", "event", "activity", "campaign", "request_point_cap")}
        print(f"    meta_keys={list(md.keys())[:20]}")
        print(f"    meta_pick={json.dumps(interesting, ensure_ascii=False)}")
