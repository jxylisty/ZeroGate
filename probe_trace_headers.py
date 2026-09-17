import re
src = open(r'D:\CodeBuddy\resources\app\extensions\genie\out\webviews\components.ebc7c804.chunk.js', encoding='utf-8', errors='replace').read()
print('len', len(src))
i = src.find('ensureChatCompletionTraceHeaders')
print('at', i)
if i >= 0:
    s = max(0, i-1500); e = min(len(src), i+1500)
    print(src[s:e])