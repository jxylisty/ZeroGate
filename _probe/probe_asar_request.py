# -*- coding: utf-8 -*-
"""挖 app.asar 里 request$1 的定义（headers 拼装 + baseURL）。"""
import re

raw = open(r"D:\autoclaw\resources\app.asar", "rb").read()
i = raw.find(b"autoclaw-newbie-guide/token")
window = raw[max(0, i - 150000): i].decode("utf-8", errors="replace")

needle = "request$1"
p = window.rfind(needle)
found = False
while p >= 0:
    seg = window[p:p + 3000]
    if "headers" in seg and ("fetch" in seg or "method" in seg or "POST" in seg):
        print(f"=== request$1 @ window:{p} ===")
        print(seg.replace("\n", " | ")[:3000])
        found = True
        break
    p = window.rfind(needle, 0, p)
if not found:
    # 直接找带 X-Auth-Sign 的请求头构造
    p2 = window.rfind("X-Auth-Sign")
    if p2 >= 0:
        print("=== X-Auth-Sign context ===")
        print(window[max(0, p2 - 1500):p2 + 1500].replace("\n", " | "))
