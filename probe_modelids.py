import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

pats = [r"gemini-2\.5", r"gpt-4o-mini", r"claude-sonnet-4-5", r"glm-4\.5", r"deepseek", r"chat/completions", r"modelList", r"public/models", r"v2/chat/completions\?*.*== *\"([a-z" ]
seen = set()
for pat in pats:
    print("="*34, pat)
    cnt = 0
    for i, m in enumerate(re.finditer(pat, data)):
        s = max(0, m.start()-130)
        e = min(len(data), m.end()+240)
        ctx = re.sub(r"\s+", " ", data[s:e])
        key = ctx[:160]
        if key in seen:
            continue
        seen.add(key)
        print("@", m.start(), ctx[:400])
        print()
        cnt += 1
        if cnt >= 4:
            break