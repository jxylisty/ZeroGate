import sqlite3, json, re

db = r'C:\Users\zzx05\AppData\Roaming\CodeBuddy\User\globalStorage\state.vscdb'
con = sqlite3.connect(db)
cur = con.cursor()
cur.execute("SELECT value FROM ItemTable WHERE key='Tencent-Cloud.coding-copilot'")
row = cur.fetchone()
val = row[0].decode('utf-8', 'replace') if isinstance(row[0], bytes) else row[0]
print('len:', len(val))
# look for JWT-looking tokens or auth json blobs
jwts = re.findall(r'eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+', val)
print('JWT candidates:', len(jwts))
for j in jwts[:5]:
    payload = j.split('.')[1]
    pad = '=' * (-len(payload) % 4)
    try:
        print('  payload:', json.loads(base64.urlsafe_b64decode(payload + pad)))
    except Exception as e:
        print('  decode err', e)
# also find any "auth" nested structure indexes
for m in re.finditer(r'"auth"', val):
    s = max(0, m.start()-200); e = min(len(val), m.start()+400)
    print('..."auth" context...', val[s:e][:600].replace('\\n',' '))
    print('----')