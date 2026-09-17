import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

# module 282922 starts at offset 5641424 area — dump 20KB from there
s0 = 5641424
print("########## MODULE 282922 DUMP ##########")
seg = data[s0:s0+20000]
seg = re.sub(r"\s+", " ", seg)
print(seg)