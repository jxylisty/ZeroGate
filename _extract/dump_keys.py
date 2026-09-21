import sqlite3, json, re

db = r'C:\Users\zzx05\AppData\Roaming\CodeBuddy\User\globalStorage\state.vscdb'
con = sqlite3.connect(db)
cur = con.cursor()
cur.execute("SELECT key, value FROM ItemTable")
rows = cur.fetchall()
for k, v in rows:
    try:
        s = v.decode('utf-8', 'replace')
    except Exception:
        s = str(v)
    if 'LastVaultPassphraseKey' in s or 'lastVaultPassphrase' in s or 'vscode-machine-id' in s or 'machineId' in k:
        print('KEY', k, '->', s[:300]); print('----')
# also scan for very long random hex/base64 looking values that could be keys
for k, v in rows:
    try:
        s = v.decode('utf-8', 'replace')
    except Exception:
        continue
    for m in re.finditer(r'[A-Za-z0-9+/=]{40,}', s):
        print('BLOB at', k, '::', m.group()[:60])
        break