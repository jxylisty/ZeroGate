import sqlite3, json, ctypes, base64
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

def dpapi_unprotect(blob):
    class DATA_BLOB(ctypes.Structure):
        _fields_ = [("cbData", ctypes.c_ulong), ("pbData", ctypes.c_void_p)]
    inb = DATA_BLOB(len(blob), ctypes.cast(ctypes.create_string_buffer(blob, len(blob)), ctypes.c_void_p))
    outb = DATA_BLOB()
    if not ctypes.windll.crypt32.CryptUnprotectData(ctypes.byref(inb), None, None, None, None, 0, ctypes.byref(outb)):
        raise RuntimeError('fail')
    data = ctypes.string_at(outb.pbData, outb.cbData)
    ctypes.windll.kernel32.LocalFree(ctypes.c_void_p(outb.pbData))
    return data

ls = json.load(open(r'C:\Users\zzx05\AppData\Roaming\CodeBuddy\Local State', encoding='utf-8'))
ek = base64.b64decode(ls['os_crypt']['encrypted_key'])
dkey = dpapi_unprotect(ek[5:])

con = sqlite3.connect(r'C:\Users\zzx05\AppData\Roaming\CodeBuddy\User\globalStorage\state.vscdb')
cur = con.cursor()
cur.execute("SELECT value FROM ItemTable WHERE key LIKE '%accessToken%'")
obj = json.loads(cur.fetchone()[0])  # must be the accessToken one -> 6977
blob = bytes(obj['data'])
cipher = blob[3:]
nonce = cipher[:12]
ct = cipher[12:]
print(type(nonce), len(nonce), type(ct), len(ct))
aes = AESGCM(dkey)
pt = aes.decrypt(nonce, ct, b'')
session = json.loads(pt)
print('OK', list(session.keys()))
with open(r'D:\wild-work\data\codebuddy_intl_session.json', 'w', encoding='utf-8') as f:
    json.dump(session, f, ensure_ascii=False, indent=1)
print('saved')
at = session['auth']['accessToken']
if at.startswith('eyJ'):
    print('at', at[:60])
else:
    print('at raw head', at[:60])