import json, uuid, http.client, ssl

S = json.load(open(r"D:\wild-work\data\codebuddy_intl_session.json", encoding="utf-8"))
auth = S["auth"]; acc = S["account"]
tok = auth["accessToken"]; uid = acc["uid"]; domain = auth.get("domain", "www.codebuddy.ai")
uuidhex = uuid.uuid4().hex

base_hdr = {
    "Authorization": "Bearer " + tok,
    "X-User-Id": uid,
    "X-Domain": domain,
    "X-Product": "SaaS",
    "X-Product-Code": "codebuddy",
    "X-IDE-Name": "CodeBuddyIDE",
    "X-Requested-With": "XMLHttpRequest",
    "User-Agent": "CodeBuddyIDE/3.10.0",
    "Accept": "application/json",
}

paths = [
    "/v2/chat/models",
    "/v2/models",
    "/v2/models/list",
    "/v2/products/config",
    "/v2/config",
    "/v2/accounts",
    "/v2/plugin/login/account",
    "/v2/tools/models",
    "/v2/chat/config",
]
ctx = ssl.create_default_context()
for p in paths:
    conn = http.client.HTTPSConnection("www.codebuddy.ai", 443, timeout=30, context=ctx)
    try:
        conn.request("GET", p, headers=base_hdr)
        r = conn.getresponse()
        raw = r.read(900)
        print(f"### {p} -> {r.status} {r.reason}")
        print("   ", raw[:450])
    except Exception as e:
        print(f"### {p} -> EXC {type(e).__name__}: {e}")
    finally:
        conn.close()
print("done")