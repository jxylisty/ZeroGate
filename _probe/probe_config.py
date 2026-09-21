import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

# find waitConfiguration def
for i, m in enumerate(re.finditer(r"waitConfiguration", data)):
    s = max(0, m.start()-300)
    e = min(len(data), m.end()+700)
    ctx = data[s:e]
    if re.search(r"async waitConfiguration|\(\)\{", ctx):
        print("### @", m.start())
        print(re.sub(r"\s+", " ", ctx)[:1000])
        print()

# find product config fetch URL patterns
for pat in [r"/v2/products/", r"product/config", r"config-with", r"fetchProductConfig", r"configurationService"]:
    print("="*30, pat)
    for i, m in enumerate(re.finditer(pat, data)):
        s = max(0, m.start()-200)
        e = min(len(data), m.end()+300)
        ctx = re.sub(r"\s+", " ", data[s:e])
        print("@", m.start(), ctx[:380])
        print()
        if i > 4:
            break