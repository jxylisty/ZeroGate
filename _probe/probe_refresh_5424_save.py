# -*- coding: utf-8 -*-
"""刷新 5424 token 并写回 auth 文件（嵌套格式，备份 .bak2）。"""
import hashlib
import json
import shutil
import time
import urllib.request

AUTH = r"d:\wild-work\src-true\auths\autoclaw-54243632af92476b232f27e0a0380505.json"
BASE = "https://autoglm-acceleration-api.zhipuai.cn"
APPID = "100003"
SECRET = "38d2391985e2369a5fb8227d8e6cd5e5"

with open(AUTH, encoding="utf-8") as f:
    doc = json.load(f)
auth = doc["auth"]

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
        print("HTTP", resp.status, "code:", data.get("code"), "msg:", data.get("msg"))
        out = data.get("data") or {}
        new_at = (out.get("access_token") or "").strip()
        new_rt = (out.get("refresh_token") or "").strip()
        if data.get("code") == 0 and new_at:
            # 备份当前文件
            shutil.copyfile(AUTH, AUTH + ".bak2")
            # 写回嵌套格式（与解析端字段对齐）
            auth["accessToken"] = new_at
            if new_rt:
                auth["refreshToken"] = new_rt
            doc["auth"] = auth
            with open(AUTH, "w", encoding="utf-8") as f:
                json.dump(doc, f, ensure_ascii=False, indent=2)
            print("SAVED. new access_token:", new_at[:24] + "...")
            print("refresh_token rotated:", bool(new_rt and new_rt != rt))
        else:
            print("refresh rejected:", json.dumps(data, ensure_ascii=False)[:300])
            print("需要重新登录（RT 可能一次性已被首次调用消费）")
except urllib.error.HTTPError as e:
    print("HTTP", e.code, e.read().decode()[:300])
except Exception as e:
    print("ERR", e)
