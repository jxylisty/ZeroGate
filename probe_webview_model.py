import re, glob, os

files = glob.glob(r"D:\CodeBuddy\resources\app\extensions\genie\out\webviews\*.chunk.js") + glob.glob(r"D:\CodeBuddy\resources\app\extensions\genie\out\webviews\*.js")
print(files)
for f in files:
    print("="*60, f, os.path.getsize(f))
    try:
        data = open(f, encoding="utf-8", errors="replace").read()
    except Exception as e:
        print("ERR", e); continue
    for pat in [r"sonnet", r"claude-", r"modelId", r"serviceId", r"service_id", r"model_list", r"models\s*:"]:
        hits = list(re.finditer(pat, data))[:3]
        if hits:
            print("  PAT", pat, "hits:", len(hits))
            for m in hits[:2]:
                s = max(0, m.start()-100)
                e = min(len(data), m.end()+200)
                print("   @", m.start(), re.sub(r"\s+"," ",data[s:e])[:320])
            print()