import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

s0 = 10133153 - 2600
seg = data[s0:s0+6000]
seg = re.sub(r"\s+", " ", seg)
print(seg)