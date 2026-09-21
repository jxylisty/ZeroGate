import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

idx = data.find('538647:(')
if idx == -1:
    idx = data.find('538647:', 16370000)
print("module header @", idx)
if idx >= 0:
    seg = data[idx:idx+4500]
    print(re.sub(r"\s+", " ", seg))