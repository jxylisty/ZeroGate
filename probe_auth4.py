import re
src = open(r'D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js', encoding='utf-8', errors='replace').read()

def ctx(pat, n=4, w=700):
    ms = list(re.finditer(pat, src))
    print(f'===== {pat}: {len(ms)} hits')
    for m in ms[:n]:
        s = max(0, m.start()-w); e = min(len(src), m.end()+w)
        print(src[s:e])
        print('---')

ctx(r'enterpriseHeaders\(', 2, 500)
ctx(r'getAccounts\(tn', 2, 800)
ctx(r'fetchTokenByTmpCode', 2, 900)