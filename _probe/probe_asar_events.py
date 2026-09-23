# -*- coding: utf-8 -*-
"""挖 reportWebEvent 调用现场的 event_type 枚举 + identity-tasks 参数。"""
import re

raw = open(r"D:\autoclaw\resources\app.asar", "rb").read()

print("==== reportWebEvent 调用现场 ====")
for m in list(re.finditer(rb"reportWebEvent\(", raw))[:10]:
    i = m.start()
    ctx = raw[max(0, i - 350): i + 350].decode("utf-8", errors="replace").replace("\n", " | ")
    print(f"--- @ {i} ---")
    print(ctx)
    print()

print("==== event_type 字符串枚举 ====")
for m in list(re.finditer(rb'event_type["\s:]+([a-zA-Z_\-"]{3,40})', raw))[:15]:
    print(m.group(0).decode("utf-8", errors="replace"))

print()
print("==== makeNewbieParams / identity-tasks 现场 ====")
i = raw.find(b"identity-tasks/complete")
ctx = raw[max(0, i - 2500): i + 200].decode("utf-8", errors="replace").replace("\n", " | ")
print(ctx[-2700:])
