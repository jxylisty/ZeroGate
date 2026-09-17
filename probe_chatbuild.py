import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

targets = ["buildAuthHeaders", "sendChatCompletion", "X-Product-Token", "X-Conversation-Request-ID", "completionsPath", "X-Auth-Refresh-Source"]
CAP = 2
for pat in targets:
    print("=" * 40, pat)
    for i, m in enumerate(re.finditer(pat, data)):
        if i >= CAP:
            break
        s = max(0, m.start() - 350)
        e = min(len(data), m.end() + 450)
        ctx = re.sub(r"\s+", " ", data[s:e])
        print(f"--- hit {i} @ {m.start()}")
        print(ctx[:800])
        print()