import json, http.client, ssl

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
for p in ["/v2/config", "/v3/config", "/v2/config/", "/config/models", "/v2/config?platform=CLI", "/v2/config?clientType=IDE"]:
    conn = http.client.HTTPSConnection("www.codebuddy.ai", 443, timeout=40, context=ctx)
    try:
        conn.request("GET", p, headers=hdr)
        r = conn.getresponse()
        raw = r.read(1200)
        print(f"### {p} -> {r.status}")
        print("   ", raw[:500])
    except Exception as e:
        print(f"### {p} -> EXC {type(e).__name__}: {e}")
    finally:
        conn.close()
print("done")