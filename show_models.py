import json

cfg = json.load(open(r"D:\wild-work\data\codebuddy_intl_config.json", encoding="utf-8"))
data = cfg["data"]
for m in data.get("models", []):
    mid = m.get("id")
    rel = m.get("relatedModels")
    promos = [p for p in data.get("modelPromotions", []) if p.get("model") == mid or p.get("modelId") == mid]
    print("=" * 20, mid)
    if rel:
        for r in rel[:6]:
            print("   rel:", json.dumps(r, ensure_ascii=False)[:300])
    for p in promos[:6]:
        print("   promo:", json.dumps(p, ensure_ascii=False)[:300])
    print("   keys:", list(m.keys()))
    print("   ", json.dumps(m, ensure_ascii=False)[:500])