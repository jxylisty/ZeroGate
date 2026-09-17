import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

for i, m in enumerate(re.finditer(r"TencentAPIImpl", data)):
    s = max(0, m.start()-120)
    print(i, "@", m.start(), "..", re.sub(r"\s+"," ",data[s:s+220]))
print()
# find the class definition module: 'class.*TencentAPIImpl' or 'TencentAPIImpl=class'
for i, m in enumerate(re.finditer(r"TencentAPIImpl(?:=|;|\s)", data)):
    s0 = m.start()
    chunk = data[s0:s0+30000]
    if "class " in chunk[:200] or "let " in chunk[:200]:
        print("=== likely def @", s0)
        print(re.sub(r"\s+", " ", chunk[:30000])[:4000])
        break