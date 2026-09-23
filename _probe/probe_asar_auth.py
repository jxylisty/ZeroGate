# -*- coding: utf-8 -*-
"""挖 authHeaders 定义（签名算法细节：毫秒/秒、参与签名的头集合）。"""
raw = open(r"D:\autoclaw\resources\app.asar", "rb").read()

j = raw.find(b"function authHeaders")
if j < 0:
    j = raw.find(b"authHeaders = ")
print(f"=== authHeaders @ {j} ===")
print(raw[j:j + 2600].decode("utf-8", errors="replace").replace("\n", " | "))
