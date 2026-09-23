# -*- coding: utf-8 -*-
"""搜 asar：当天有效/积分加油站弹窗逻辑 + point_grant。"""
import re

raw = open(r"D:\autoclaw\resources\app.asar", "rb").read()

for kw in [b"\xe5\xbd\x93\xe5\xa4\xa9\xe6\x9c\x89\xe6\x95\x88",  # 当天有效
           b"point_grant",
           b"\xe7\xa7\xaf\xe5\x88\x86\xe5\x8a\xa0\xe6\xb2\xb9\xe7\xab\x99",  # 积分加油站
           ]:
    hits = [m.start() for m in re.finditer(re.escape(kw), raw)][:6]
    print(f"#### kw={kw.decode('utf-8', errors='replace')} hits={len(hits)}")
    for i in hits:
        ctx = raw[max(0, i - 600): i + 600].decode("utf-8", errors="replace").replace("\n", " | ")
        print(f"--- @ {i} ---")
        print(ctx)
        print()
