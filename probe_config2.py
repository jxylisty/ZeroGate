import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

for i, m in enumerate(re.finditer(r'"/v2/config"', data)):
    s = max(0, m.start()-500)
    e = min(len(data), m.end()+600)
    ctx = re.sub(r"\s+", " ", data[s:e])
    print(f"--- @ {m.start()}")
    print(ctx[:1000])
    print()