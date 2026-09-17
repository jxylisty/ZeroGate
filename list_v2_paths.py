import re
src = open(r'D:\CodeBuddy\resources\app\extensions\genie\out\extension\index.js', encoding='utf-8', errors='replace').read()

# all distinct URL substrings containing /v2/ or completions
hits = set(re.findall(r'["\'`](/v2/[A-Za-z0-9_\-/{}:$*]+)["\'`]', src))
for x in sorted(hits):
    print(x)
print('---completions-ish---')
for x in sorted(h for h in hits if 'complete' in h.lower() or 'chat' in h.lower() or 'agent' in h.lower()):
    print(x)