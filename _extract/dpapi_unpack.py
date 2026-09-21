import sqlite3, json, ctypes, base64

db = r'C:\Users\zzx05\AppData\Roaming\CodeBuddy\User\globalStorage\state.vscdb'
con = sqlite3.connect(db)
cur = con.cursor()
cur.execute("SELECT key, value FROM ItemTable WHERE key LIKE 'secret://%accessToken%'")
rows = cur.fetchall()
print('rows:', len(rows))
for k, v in rows:
    obj = json.loads(v)          # {"type":"Buffer","data":[...]}
    blob = bytes(obj['data'])
    print('key:', k)
    print('len:', len(blob), 'head:', blob[:6])
    if blob[:3] == b'v10':
        cipher = blob[3:]
    else:
        cipher = blob
    # Try DPAPI CurrentUser unprotect
    class DATA_BLOB(ctypes.Structure):
        _fields_ = [("cbData", ctypes.c_ulong), ("pbData", ctypes.c_void_p)]
    def unprotect(enc):
        h = ctypes.cdll.Crypt32
        inb = DATA_BLOB(len(enc), ctypes.cast(ctypes.create_string_buffer(enc, len(enc)), ctypes.c_void_p))
        outb = DATA_BLOB()
        if not h.CryptUnprotectData(ctypes.byref(inb), None, None, None, None, 1, ctypes.byref(outb)):
            err = ctypes.get_last_error()
            # 0x80070057 param error usually means it's not straight DPAPI
            return None, f'CryptUnprotectData failed err={err:#x}'
        data = ctypes.string_at(outb.pbData, outb.cbData)
        ctypes.windll.kernel32.LocalFree(outb.pbData)
        return data, 'ok'
    dec, note = unprotect(cipher)
    print('  decrypt note:', note)
    if dec:
        print('  decrypted head:', dec[:200])
        try:
            print('  decrypted json:', json.loads(dec))
        except Exception:
            print('  (not json, raw)')
else:
    print('no accessToken secret row')