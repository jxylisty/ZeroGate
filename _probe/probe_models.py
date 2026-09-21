import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

pats = [r"/v2/models[^a-z]", r"chatModels", r"fetchModelConfig", r"/v2/agents", r"service info not found", r"modelListPath", r"getModelConfig"]
seen = set()
for pat in pats:
    print("="*36, pat)
    for i, m in enumerate(re.finditer(pat, data)):
        s = max(0, m.start()-160)
        e = min(len(data), m.end()+260)
        ctx = re.sub(r"\s+", " ", data[s:e])
        key = ctx[:200]
        if key in seen:
            continue
        seen.add(key)
        print("--- @", m.start(), ctx[:420])
        print()
        if i > 5:
            break