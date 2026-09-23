# -*- coding: utf-8 -*-
"""小号 1a09：钱包实例全景 + ledgers 换个口径搜 token 型入账记录。"""
import json, time, hashlib, urllib.request, glob

SECRET = "38d2391985e2369a5fb8227d8e6cd5e5"
BASE = "https://autoglm-acceleration-api.zhipuai.cn"

def load(path):
    j = json.load(open(path, encoding="utf-8"))
    tok = ((j.get("auth") or {}).get("accessToken") or j.get("accessToken") or "").replace("Bearer ", "")
    return tok

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
    if "1a09c829" in p:
        tok = load(p)
        break

print("== 钱包实例全景（全部 asset_type）==")
raw = get(BASE + "/agent-assetmgr/api/v1/wallet-instances?wallet_type=all&wallet_scope=all", tok)
insts = raw["data"].get("wallet_instances", [])
print(f"共 {len(insts)} 个实例：")
seen = {}
for it in insts:
    key = (it.get("asset_type"), it.get("public_wallet_type"), it.get("wallet_name"))
    seen[key] = seen.get(key, 0) + it.get("balance", 0)
    # 逐条打印非常驻（小余额的碎片包全部展示）
print("按类型汇总：")
for (at, pwt, name), bal in sorted(seen.items()):
    print(f"  asset_type={at} public={pwt} name={name} -> 合计 {bal}")

print()
print("== 3亿 tokens 会落哪：查 subscription/other 类型实例明细 ==")
for it in insts:
    if it.get("public_wallet_type") in ("subscription", "other", "fuel_pack") or (it.get("balance") or 0) > 100000:
        print(json.dumps(it, ensure_ascii=False)[:400])

print()
print("== ledgers_std 按 asset_type=全部口径（看有没有 token 型 entry）==")
for at in ("point", "token", "all"):
    try:
        led = get(BASE + f"/agent-assetmgr/api/v1/ledgers_std?asset_type={at}&wallet_type=all&cursor=&page_size=6", tok)
        entries = (led.get("data") or {}).get("entries", [])
        print(f"-- asset_type={at}: {len(entries)} entries")
        for e2 in entries[:6]:
            print(f"   {e2.get('occurred_at','')} {e2.get('mutation_type')} {e2.get('amount')} {str(e2.get('description',''))[:40]}")
    except Exception as ex:
        print(f"-- asset_type={at}: ERR {ex}")
