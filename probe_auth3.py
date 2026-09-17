import re
src = open(r'D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js', encoding='utf-8', errors='replace').read()

def ctx(pat, n=3, w=600):
    ms = list(re.finditer(pat, src))
    print(f'===== {pat}: {len(ms)} hits')
    for m in ms[:n]:
        s = max(0, m.start()-w); e = min(len(src), m.end()+w)
        print(src[s:e])
        print('---')

# where does the IDE store the account auth / how does it load session?
for pat in [r'login/account', r'loginAccount', r'X-Auth-Refresh-Source', r'token/refresh', r'account/refresh', r'auth/refresh', r'plugin/login']:
    ctx(pat, 3, 500)