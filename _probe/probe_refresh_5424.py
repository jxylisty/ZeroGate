# -*- coding: utf-8 -*-
"""探测 5424 的 refresh_token 是否仍有效。"""
import hashlib
import json
import time
import urllib.request

AUTH = r"d:\wild-work\src-true\auths\autoclaw-54243632af92476b232f27e0a0380505.json"
BASE = "https://autoglm-acceleration-api.zhipuai.cn"
APPID = "100003"
SECRET = "38d2391985e2369a5fb8227d8e6cd5e5"

with open(AUTH, encoding="utf-8") as f:
    auth = json.load(f)["auth"]

dev = auth["deviceId"]
rt = auth["refreshToken"].removeprefix("Bearer ")
ts = str(int(time.time()))
sign = hashlib.md5(f"{APPID}&{ts}&{SECRET}".encode()).hexdigest()

body = json.dumps({
    "source_id": "autoclaw",
    "device_id": dev,
    "refresh_token": rt,
}).encode()

req = urllib.request.Request(BASE + "/userapi/v1/refresh", data=body, method="POST")
for k, v in {
    "Content-Type": "application/json",
    "Accept": "*/*",
    "X-Version": "1.18.4",
    "X-Tm": "web",
    "X-Product": "autoclaw",
    "X-Client-Type": "web",
    "X-Channel": "official",
    "X-Auth-Appid": APPID,
    "X-Auth-TimeStamp": ts,
    "X-Auth-Sign": sign,
}.items():
    req.add_header(k, v)

try:
    with urllib.request.urlopen(req, timeout=20) as resp:
        data = json.loads(resp.read().decode())
        print("HTTP", resp.status)
        out = data.get("data") or {}
        print("code:", data.get("code"), "msg:", data.get("msg"))
        new_at = out.get("access_token") or ""
        new_rt = out.get("refresh_token") or ""
        print("new access_token:", (new_at[:24] + "...") if new_at else "(empty)")
        print("new refresh_token rotated:", bool(new_rt and new_rt != rt))
        if new_at:
            with open(AUTH + ".bak", "w", encoding="utf-8") as f:
                json.dump(auth, f, ensure_ascii=False, indent=2)
            print("backup saved:", AUTH + ".bak")
except urllib.error.HTTPError as e:
    print("HTTP", e.code, e.read().decode()[:300])
except Exception as e:
    print("ERR", e)
