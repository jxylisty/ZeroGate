import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

# find definition site: 'buildAuthHeaders' preceded by 'async ' or '=' etc.
for m in re.finditer(r"buildAuthHeaders", data):
    s = max(0, m.start()-500)
    e = min(len(data), m.end()+700)
    pre = data[s:m.start()]
    # definition token candidates
    if re.search(r"async\s+$|=\s*function|=\s*async", data[max(0,m.start()-80):m.start()]) or "class " not in "".join([data[max(0,m.start()-3000):m.start()]]):
        pass
    if "buildAuthHeaders=function" in data[max(0,m.start()-60):m.start()+20] or "async buildAuthHeaders" in data[max(0,m.start()-60):m.start()+20]:
        ctx = re.sub(r"\s+", " ", data[s:e])
        print("=== DEF @", m.start())
        print(ctx[:1200])
        print()
        break

# also print all buildAuthHeaders call sites briefly
print("### call sites ###")
for i, m in enumerate(re.finditer(r"buildAuthHeaders", data)):
    if i > 30:
        break
    s = max(0, m.start()-120)
    e = min(len(data), m.end()+80)
    ctx = re.sub(r"\s+", " ", data[s:e])
    print(i, "@", m.start(), "...", ctx[-200:])