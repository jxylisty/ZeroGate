import re
src = open(r'D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js', encoding='utf-8', errors='replace').read()

# The enum maps GET_WEBVIEW_INFO to getWebviewInfo command id string; find a handler with async ... getWebviewInfo
ms = list(re.finditer(r'getWebviewInfo', src))
print('hits', len(ms))
for m in ms:
    s = m.start()
    seg = src[max(0,s-300):s+800]
    print('@', s, '::', seg)
    print('--------')