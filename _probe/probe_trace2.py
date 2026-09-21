import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

# module 538647 define area
m = re.search(r"ar\.ensureChatCompletionTraceHeaders=function ensureChatCompletionTraceHeaders[^}]{0,4000}", data)
if m:
    print(m.group(0)[:3500])
else:
    # dump around 16387117 area
    s = 16387117 - 2000
    print(re.sub(r"\s+", " ", data[s:s+7000]))