import sqlite3, json, base64

db = r'C:\Users\zzx05\AppData\Roaming\CodeBuddy\User\globalStorage\state.vscdb'
con = sqlite3.connect(db)
cur = con.cursor()
cur.execute("SELECT key, value FROM ItemTable WHERE key LIKE 'secret://%' OR key LIKE 'secret://%%accessToken%%'")
for k, v in cur.fetchall():
    if 'accessToken' in k or 'ACC_' in k or 'Product' in k or 'Endpoint' in k or 'LLM' in k:
        print('==== key:', k)
        print('value head:', v[:400])
        print()

# Also dump the big Tencent-Cloud.coding-copilot global state (not secret) - look for token-like fields
cur.execute("SELECT key, value FROM ItemTable WHERE key='Tencent-Cloud.coding-copilot'")
row = cur.fetchone()
if row:
    val = row[1]
    # try to find token-ish substrings
    import re
    hits = re.findall(r'[a-z0-9_\-\.]*(?:token|access|refresh|auth)[a-z0-9_\-\.]*', val, re.I)
    print('== keys mentioning token in big state:', sorted(set(hits))[:30])