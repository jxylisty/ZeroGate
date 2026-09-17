import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

pats = [r"/v2/configurations", r"/v2/configuration", r"/v2/products", r"/v2/codebuddy-config", r"/v2/preset", r"/v2/app-config", r"/v2/config/", r"configurationPath", r"configurations/", r"/v2/current", r"/v2/me/config"]
seen = set()
for pat in pats:
    for i, m in enumerate(re.finditer(pat, data)):
        s = max(0, m.start()-180)
        e = min(len(data), m.end()+260)
        ctx = re.sub(r"\s+", " ", data[s:e])
        key = ctx[:170]
        if key in seen:
            continue
        seen.add(key)
        print(pat, "@", m.start())
        print("  ", ctx[:420])
        print()
        if i > 3:
            break