import re

src = open(r'D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js', encoding='utf-8', errors='replace').read()
print('src len', len(src))
for pat in ['planning-genie', 'chat/completions']:
    print('\n=====', pat, '=====')
    for m in list(re.finditer(re.escape(pat), src))[:6]:
        s = max(0, m.start()-700); e = min(len(src), m.end()+700)
        print(src[s:e][:1400])
        print('-------')