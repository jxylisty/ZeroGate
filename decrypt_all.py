import sqlite3, json, ctypes, base64

def dpapi_unprotect(blob: bytes) -> bytes:
    class DATA_BLOB(ctypes.Structure):
        _fields_ = [("cbData", ctypes.c_ulong), ("pbData", ctypes.c_void_p)]
    inb = DATA_BLOB(len(blob), ctypes.cast(ctypes.create_string_buffer(blob, len(blob)), ctypes.c_void_p))
    outb = DATA_BLOB()
    if not ctypes.windll.crypt32.CryptUnprotectData(ctypes.byref(inb), None, None, None, None, 0, ctypes.byref(outb)):
        raise RuntimeError(f'CryptUnprotectData failed {ctypes.get_last_error():#x}')
    data = ctypes.string_at(outb.pbData, outb.cbData)
    ctypes.windll.kernel32.LocalFree(ctypes.c_void_p(outb.pbData))
    return data

from cryptography.hazmat.primitives.ciphers.aead import AESGCM
ls = json.load(open(r'C:\Users\zzx05\AppData\Roaming\CodeBuddy\Local State', encoding='utf-8'))
ek = base64.b64decode(ls['os_crypt']['encrypted_key'])
dkey = dpapi_unprotect(ek[5:])

con = sqlite3.connect(r'C:\Users\zzx05\AppData\Roaming\CodeBuddy\User\globalStorage\state.vscdb')
cur = con.cursor()
cur.execute("SELECT key, value FROM ItemTable WHERE key LIKE 'secret://%'")
rows = cur.fetchall()
print('total secret rows:', len(rows))
out = {}
for k, v in rows:
    try:
        obj = json.loads(v)
        blob = bytes(obj['data'])
    except Exception:
        continue
    if blob[:3] == b'v10':
        nonce, ct = blob[3:15], blob[15:]
        try:
            pt = AESGCM(dkey).decrypt(nonce, ct, b'')
        except Exception as e:
            print('decrypt fail for', k[:80], e); continue
        name = k.split('"key":"')[1].rstrip('"}') if '"key":"' in k else k[:40]
        out[name] = pt
        print('====', name, '->', pt[:250])
        print()

# if accessToken key found, save structured
at = out.get('planning-genie.new.accessToken')
if at:
    try:
        data = json.loads(at)
        print('TOP structure keys:', list(data.keys()) if isinstance(data, dict) else type(data))
        print(json.dumps({kk: (vv if not isinstance(vv,(dict,list)) else '<%s %d>'%(type(vv).__name__, len(vv))) for kk,vv in data.items()}, ensure_ascii=False, indent=1)[:2500])
    except Exception as e:
        print('not json:', e)