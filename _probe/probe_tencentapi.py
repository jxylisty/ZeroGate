import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

s0 = 20520205 - 3000
seg = data[s0:s0+15000]
seg = re.sub(r"\s+", " ", seg)
print(seg)