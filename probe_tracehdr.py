import re

p = r"D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js"
data = open(p, encoding="utf-8", errors="replace").read()

def dump(offset, length, title):
    print("#"*30, title, "@", offset)
    print(re.sub(r"\s+", " ", data[offset:offset+length]))

# OpenAIAPIImpl chat area
dump(496559 - 200, 7000, "OpenAIAPIImpl module")

# find ensureChatCompletionTraceHeaders module 538647
for i, m in enumerate(re.finditer(r"ensureChatCompletionTraceHeaders", data)):
    s = max(0, m.start()-1000)
    e = m.end()+3000
    seg = data[s:e]
    # find the definition: 'exports.ensureChatCompletionTraceHeaders=function' or 'ar.ensureChatCompletionTraceHeaders'
    mm = re.search(r"ensureChatCompletionTraceHeaders\s*=.*?(?=\n\s*(?:26|25)\d{4}:|\Z)", seg)
    print("HIT", i, "@", m.start())
    defm = re.search(r"(?:ar|exports|module\.exports)\.ensureChatCompletionTraceHeaders[^;]{0,2500}", seg, re.S)
    if defm:
        print(defm.group(0)[:2200])
        break