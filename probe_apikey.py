import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

pats = [r"X-Api-Key", r"fetchRiskDetect", r"X-Device-Token"]
CAP = 3
for pat in pats:
    print("=" * 40, pat)
    for i, m in enumerate(re.finditer(pat, data)):
        if i >= CAP:
            break
        s = max(0, m.start() - 300)
        e = min(len(data), m.end() + 380)
        ctx = re.sub(r"\s+", " ", data[s:e])
        print(f"--- hit {i} @ {m.start()}")
        print(ctx[:700])
        print()