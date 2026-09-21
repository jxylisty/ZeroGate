import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

for pat in ["/config/models", "/v3/config", "getEndpoint()", "waitConfiguration(){", "/config/agents"]:
    print("="*34, pat)
    cnt = 0
    for m in re.finditer(re.escape(pat), data):
        s = max(0, m.start()-250)
        e = min(len(data), m.end()+350)
        ctx = re.sub(r"\s+", " ", data[s:e])
        print("--- @", m.start(), ctx[:560])
        print()
        cnt += 1
        if cnt >= 3:
            break