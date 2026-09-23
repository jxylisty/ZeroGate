# -*- coding: utf-8 -*-
"""挖 commonHeaders 与 getApiRequestHost 定义。"""
raw = open(r"D:\autoclaw\resources\app.asar", "rb").read()

j = raw.find(b"function commonHeaders")
if j < 0:
    j = raw.find(b"commonHeaders = ")
print(f"=== commonHeaders @ {j} ===")
print(raw[j:j + 2400].decode("utf-8", errors="replace").replace("\n", " | "))
print()

k = raw.find(b"function getApiRequestHost")
if k < 0:
    k = raw.find(b"getApiRequestHost = ")
print(f"=== getApiRequestHost @ {k} ===")
print(raw[k:k + 900].decode("utf-8", errors="replace").replace("\n", " | "))
