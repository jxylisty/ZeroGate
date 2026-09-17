import json, http.client, ssl, re

S = json.load(open(r"D:\wild-work\data\codebuddy_intl_session.json", encoding="utf-8"))
auth = S["auth"]; acc = S["account"]
tok = auth["accessToken"]; uid = acc["uid"]; domain = auth.get("domain", "www.codebuddy.ai")

hdr = {
    "Authorization": "Bearer " + tok,
    "X-User-Id": uid,
    "X-Domain": domain,
    "X-Product": "SaaS",
    "X-Product-Code": "codebuddy",
    "X-IDE-Name": "CodeBuddyIDE",
    "X-IDE-Version": "3.10.0",
    "X-Requested-With": "XMLHttpRequest",
    "User-Agent": "CodeBuddyIDE/3.10.0",
    "Accept": "application/json",
}
ctx = ssl.create_default_context()
conn = http.client.HTTPSConnection("www.codebuddy.ai", 443, timeout=60, context=ctx)
conn.request("GET", "/v3/config", headers=hdr)
r = conn.getresponse()
raw = r.read()
print("status:", r.status, "len:", len(raw))
conn.close()

cfg = json.loads(raw)
data = cfg.get("data", {})
print("top keys:", list(data.keys()))
models = data.get("models", [])
print("models count:", len(models))
ids = []
for m in models:
    ids.append((m.get("id"), m.get("modelId"), m.get("displayName") or m.get("name")))
for x in ids[:60]:
    print(x)
json.dump(cfg, open(r"D:\wild-work\data\codebuddy_intl_config.json", "w", encoding="utf-8"), ensure_ascii=False, indent=1)
print("saved full config")