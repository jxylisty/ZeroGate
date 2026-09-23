# -*- coding: utf-8 -*-
"""收网终验：两号 promotion 全 modal 领取扫描（双头契约）+ 任务列表 + 钱包/过期三合一。"""
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

def post(url, payload, tok):
    req = urllib.request.Request(url, data=json.dumps(payload).encode(), headers=hdr(tok), method="POST")
    try:
        r = urllib.request.urlopen(req, timeout=20)
        return r.status, r.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", errors="replace")

now = int(time.time() * 1000)
for path in sorted(glob.glob(r"D:\wild-work\src-true\auths\autoclaw-*.json")):
    tok, dev = load(path)
    print("=" * 18, path.split("\\")[-1][:18], "=" * 18)
    w = get(BASE + "/agent-assetmgr/api/v2/wallets?biz_app_id=autoclaw", tok)["data"]
    print("wallet total =", w["total_balance"])
    cfg = get(BASE + "/autoclaw-proxy/proxy/autoclaw-promotion-config", tok)["data"]
    claimed_in_window, others = [], []
    for m in cfg.get("modals", []):
        if not (m.get("startTime", 0) <= now <= m.get("endTime", 0)):
            continue
        if "已领取" in m.get("btnText", ""):
            claimed_in_window.append(m["modal_id"])
        else:
            others.append((m["modal_id"], m.get("btnText", ""), m.get("btnType", "")))
    # 已领过的窗口内活动：逐一重放确认幂等（points=0 = 今日份额确已入账过）
    for mid in claimed_in_window:
        for rt in ("sendReward", "timingReward"):
            s, b = post(BASE + "/autoclaw-proxy/proxy/autoclaw-promotion-reward",
                        {"source_id": "autoclaw", "device_id": dev, "modal_id": mid, "reward_type": rt}, tok)
            print(f"  replay {mid} {rt}: HTTP {s} {b[:110]}")
    # 其余窗口内活动也全部试一发（link 型大概率 0 分，但确认无遗漏）
    for mid, btn, btype in others:
        got = False
        for rt in ("sendReward", "timingReward"):
            s, b = post(BASE + "/autoclaw-proxy/proxy/autoclaw-promotion-reward",
                        {"source_id": "autoclaw", "device_id": dev, "modal_id": mid, "reward_type": rt}, tok)
            try:
                pts = json.loads(b).get("data", {}).get("points", 0) if s == 200 else 0
            except Exception:
                pts = 0
            if s == 200 and pts > 0:
                got = True
                print(f"  CLAIMED! {mid} {rt}: +{pts}")
        print(f"  try {mid} (btn={btn} type={btype}): {'+' if got else 'no API reward (link/未开放)'}")
    # 任务列表：固定签到任务状态
    tl = get(BASE + "/autoclaw-proxy/proxy/autoclaw-task-list", tok)["data"]
    for t in tl:
        if t.get("task_type") == "daily":
            print(f"  daily task {t['task_id']}: status={t.get('status')}")
    # 终态钱包 + 过期
    w2 = get(BASE + "/agent-assetmgr/api/v2/wallets?biz_app_id=autoclaw", tok)["data"]
    print("wallet after =", w2["total_balance"])
    try:
        e = get(BASE + "/agent-assetmgr/api/v1/points/expiring?biz_app_id=autoclaw", tok)["data"]
        print(f"expiring = {e.get('expiring_points')} (将于 {e.get('expire_time_value')} {e.get('expire_time_type')} 到期)")
    except Exception as ex:
        print("expiring ERR", ex)
    print()
