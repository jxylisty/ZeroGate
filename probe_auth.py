import re
src = open(r'D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js', encoding='utf-8', errors='replace').read()

# find AuthenticationManager class and buildAuthHeaders implementation
i = src.find('buildAuthHeaders')
print('first buildAuthHeaders at', i)
while i != -1:
    s = max(0, i-800); e = min(len(src), i+1200)
    seg = src[s:e]
    if 'X-User-Id' in seg or 'X-Domain' in seg or 'X-Device' in seg or 'X-Product' in seg:
        print(seg)
        print('=================')
    i = src.find('buildAuthHeaders', i+1)
    if i > 40000000:
        break