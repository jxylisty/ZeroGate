import sqlite3, os

DBS = [
    r'C:\Users\zzx05\AppData\Roaming\CodeBuddy\codebuddy-sessions.vscdb',
    r'C:\Users\zzx05\AppData\Roaming\CodeBuddy\User\globalStorage\state.vscdb',
]
for db in DBS:
    print('===', db)
    try:
        con = sqlite3.connect(db)
        cur = con.cursor()
        cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = [r[0] for r in cur.fetchall()]
        print('tables:', tables)
        if 'ItemTable' in tables:
            cur.execute('SELECT key, length(value) FROM ItemTable')
            for k, v in cur.fetchall()[:60]:
                print(' ', k, '->', v, 'bytes')
        elif 'DataTable' in tables:
            cur.execute('SELECT key, length(value) FROM DataTable')
            for k, v in cur.fetchall()[:60]:
                print(' ', k, '->', v, 'bytes')
    except Exception as e:
        print('ERR', e)