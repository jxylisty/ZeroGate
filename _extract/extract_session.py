import sqlite3, json, ctypes, base64, sys
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

sys.stdout.reconfigure(encoding='utf-8')

def dpapi_unprotect(blob):
    class DATA_BLOB(ctypes.Structure):
        _fields_ = [("cbData", ctypes.c_ulong), ("pbData", ctypes.c_void_p)]
    inb = DATA_BLOB(len(blob), ctypes.cast(ctypes.create_string_buffer(blob, len(blob)), ctypes.c_void_p))
    outb = DATA_BLOB()
    if not ctypes.windll.crypt32.CryptUnprotectData(ctypes.byref(inb), None, None, None, None, 0, ctypes.byref(outb)):
        raise RuntimeError('CryptUnprotectData failed')
    data = ctypes.string_at(outb.pbData, outb.cbData)
    ctypes.windll.kernel32.LocalFree(ctypes.c_void_p(outb.pbData))
    return data

ls = json.load(open(r'C:\Users\zzx05\AppData\Roaming\CodeBuddy\Local State', encoding='utf-8'))
ek = base64.b64decode(ls['os_crypt']['encrypted_key'])
dkey = dpapi_unprotect(ek[5:])
print('aes key len', len(dkey))

con = sqlite3.connect(r'C:\Users\zzx05\AppData\Roaming\CodeBuddy\User\globalStorage\state.vscdb')
cur = con.cursor()
cur.execute("SELECT value FROM ItemTable WHERE key LIKE '%accessToken%'")
cands = []
for (v,) in cur.fetchall():
    obj = json.loads(v)
    b = bytes(obj['data'])
    if len(b) > 3000:
        cands.append(b)
print('candidates', [len(c) for c in cands])
c = cands[0]
cipher = c[3:]
nonce = cipher[0:12]
ct = cipher[12:]
print('sizes', len(nonce), len(ct))
pt = AESGCM(dkey).decrypt(ct, nonce, b'')
session = json.loads(pt)
print('KEYS', list(session.keys()))
print('ACCOUNT', json.dumps(session.get('account', {}), ensure_ascii=False))
auth = session.get('auth', {})
print('AUTH', json.dumps({k: v for k, v in auth.items() if k not in ('accessToken', 'refreshToken', 'token')}, ensure_ascii=False))
print('AUTH JWT prefix', str(auth.get('accessToken', ''))[:120])
with open(r'D:\wild-work\data\codebuddy_intl_session.json', 'w', encoding='utf-8') as f:
    json.dump(session, f, ensure_ascii=False, indent=1)
print('SAVED D:\\wild-work\\data\\codebuddy_intl_session.json')