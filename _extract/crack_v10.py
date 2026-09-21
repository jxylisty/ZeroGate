import sqlite3, json, ctypes, base64, hashlib
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

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

# 1) decrypt os_crypt key
ls = json.load(open(r'C:\Users\zzx05\AppData\Roaming\CodeBuddy\Local State', encoding='utf-8'))
ek = base64.b64decode(ls['os_crypt']['encrypted_key'])
assert ek.startswith(b'DPAPI')
dkey = dpapi_unprotect(ek[5:])
print('aes key len', len(dkey), dkey[:4].hex())

# 2) load v10 secret
con = sqlite3.connect(r'C:\Users\zzx05\AppData\Roaming\CodeBuddy\User\globalStorage\state.vscdb')
cur = con.cursor()
cur.execute("SELECT value FROM ItemTable WHERE key LIKE 'secret://%accessToken%'")
obj = json.loads(cur.fetchone()[0])
blob = bytes(obj['data'])
print('blob len', len(blob), 'head', blob[:8].hex())
cipher = blob[3:]  # after v10
print('cipher len', len(cipher))

def try_aesgcm(nonce_start, aad=b''):
    aes = AESGCM(dkey)
    for off in range(nonce_start, max(nonce_start, len(cipher)-12-1)):
        nonce = cipher[off:off+12]
        ct = cipher[:off] + cipher[off+12:]
        for extra in (b'', aad):
            try:
                pt = aes.decrypt(nonce, ct + (b'' if extra==b'' else b''), extra)
                return off, pt
            except Exception:
                pass
    return None, None

# Try common layouts: nonce at start (12), or key 16 bytes then nonce
for tag, st in [('nonce_first12', 0), ('skip16_nonce', 16), ('skip16', 16)]:
    if tag == 'nonce_first12':
        nonce, ct = cipher[:12], cipher[12:]
    elif tag == 'skip16_nonce':
        nonce, ct = cipher[16:28], cipher[28:]
    else:
        nonce, ct = cipher[16:28], cipher[28:]
    for aad in (b'', b'') :
        try:
            pt = AESGCM(dkey).decrypt(nonce, ct, aad)
            print('SUCCESS layout', tag, '->', pt[:200])
            break
        except Exception as e:
            pass
    else:
        print('failed', tag)