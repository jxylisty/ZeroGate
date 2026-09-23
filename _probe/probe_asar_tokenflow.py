# -*- coding: utf-8 -*-
"""asar 深挖：Token Rush 型 token 发放的真实链路。
关键词：token 包/亿/2亿/3亿/redeem/content-resource/user-sig/兑换码/conv tokens。"""
import re

raw = open(r"D:\autoclaw\resources\app.asar", "rb").read()

def show(kw_bytes, n=4, ctx=500):
    hits = [m.start() for m in re.finditer(re.escape(kw_bytes), raw)][:n]
    print(f"#### kw={kw_bytes.decode('utf-8', errors='replace')} hits={len(hits)}")
    for i in hits:
        seg = raw[max(0, i - ctx): i + ctx].decode("utf-8", errors="replace").replace("\n", " | ")
        print(f"--- @ {i} ---")
        print(seg)
        print()
    if not hits:
        print()

# 中文活动文案关键词
show("2 亿".encode("utf-8"), 3)
show("3 亿".encode("utf-8"), 3)
show("2亿".encode("utf-8"), 3)
show("3亿".encode("utf-8"), 3)
# 兑换码 / token 发放相关接口
show(b"autoglm-codes/redeem", 2)
show(b"content-resource", 3)
show(b"user-sig", 2)
show(b"provision", 2, 400)
