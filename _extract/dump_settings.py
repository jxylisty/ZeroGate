import sqlite3, json

con = sqlite3.connect(r'C:\Users\zzx05\AppData\Roaming\CodeBuddy\User\globalStorage\state.vscdb')
cur = con.cursor()
for key in [r'CodeBuddy.settings', r'Tencent-Cloud.coding-copilot']:
    cur.execute("SELECT value FROM ItemTable WHERE key=?", (key,))
    row = cur.fetchone()
    if not row: 
        print('no', key); continue
    val = row[0]
    s = val.decode('utf-8', 'replace') if isinstance(val, bytes) else str(val)
    print('=====', key, 'len', len(s))
    # print pretty JSON if possible
    try:
        print(json.dumps(json.loads(s), ensure_ascii=False, indent=1)[:3000])
    except Exception:
        print(s[:3000])