import re
src = open(r'D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js', encoding='utf-8', errors='replace').read()

pats = {
  'secretVault': r'[Ss]ecretVault',
  'machineId+encrypt': r'getEncryptionKey|encryptValue|decryptValue|vaultEqual',
  'SecretStorage/globalState': r'contentDataKey|simpleSecretStorage|secretStorage',
  'store accessToken': r'accessToken.*(?:refreshToken|uid)|setSession|saveSession',
  'session persistence file': r'sessions\.json|auth\.json|login\.json|account\.json|\.codebuddy/auth',
}
for name, pat in pats.items():
    ms = list(re.finditer(pat, src))
    print(f'===== {name}: {len(ms)}')
    for m in ms[:4]:
        s = max(0, m.start()-260); e = min(len(src), m.end()+260)
        print(src[s:e])
        print('---')