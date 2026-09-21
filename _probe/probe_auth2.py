import re
src = open(r'D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js', encoding='utf-8', errors='replace').read()

pats = {
  'plugin/auth': r'plugin/auth',
  'auth/state': r'auth/state',
  'X-Device-Token': r'X-Device-Token',
  'X-IDE-Name': r'X-IDE-Name',
  'X-Product': r'X-Product:"SaaS"|X-Product',
  'deviceToken': r'deviceToken',
  'getDeviceToken': r'getDeviceToken|registerDevice|device/token|deviceTokenUrl',
}
for name, pat in pats.items():
    ms = list(re.finditer(pat, src))
    print(f'===== {name}: {len(ms)}')
    for m in ms[:4]:
        s = max(0, m.start()-350); e = min(len(src), m.end()+350)
        print(src[s:e])
        print('---')