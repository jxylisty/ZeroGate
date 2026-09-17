import re

src = open(r'D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js', encoding='utf-8', errors='replace').read()

# search context for 'v10' literal and secret-related crypto markers
for pat in [r'"v10"', r'v10', r'accessToken', r'getSecret', r'nacl', r'baseKey', r'sodium', r'createCipheriv', r'AES-GCM']:
    ms = list(re.finditer(pat, src))
    print(f'===== {pat}: {len(ms)} hits =====')
    for m in ms[:3]:
        s = max(0, m.start()-250); e = min(len(src), m.end()+250)
        print(src[s:e])
        print('---')