import re
src = open(r'D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js', encoding='utf-8', errors='replace').read()

# find the definition "enterpriseHeaders(" as a method: search for "enterpriseHeaders(ir)" occurrences and pick definition context
for m in list(re.finditer(r'enterpriseHeaders', src)):
    s = m.start()
    # look backwards for "class" or "(" declarations; take 200 chars before and 900 after
    before = src[max(0, s-150):s]
    seg = src[s:s+900]
    print('@', s, '::', before[-120:], '>>>', seg[:900])
    print('--------')