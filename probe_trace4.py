import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

s = 16387000
seg = data[s:s+16000]
# cut at likely module boundary: next ':(ir,ar,tn)=>' after some mark
print(re.sub(r"\s+", " ", seg)[:9000])