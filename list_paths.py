import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

paths = sorted(set(re.findall(r'"/v2/[a-zA-Z0-9_\-/{}.$]+', data)))
for pt in paths:
    print(pt)
print("total", len(paths))