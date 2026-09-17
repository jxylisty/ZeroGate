import json, http.client, ssl

S = json.load(open(r"D:\wild-work\data\codebuddy_intl_session.json", encoding="utf-8"))
auth = S["auth"]; acc = S["account"]
t = auth["accessToken"]; rt = auth["refreshToken"]; uid = acc["uid"]; domain = auth.get("domain", "www.codebuddy.ai")

ctx = ssl.create_default_context()
h = {
    "Content-Type": "application/json",
    "Authorization": "Bearer " + t,
    "X-User-Id": uid,
    "X-Domain": domain,
    "X-Product": "SaaS",
    "X-Product-Code": "codebuddy",
    "X-IDE-Name": "CodeBuddyIDE",
    "X-Requested-With": "XMLHttpRequest",
    "User-Agent": "CodeBuddyIDE/3.10.0",
    "Accept": "application/json",
    "X-Refresh-Token": rt,
    "X-Auth-Refresh-Source": "plugin",
}
conn = http.client.HTTPSConnection("www.codebuddy.ai", 443, timeout=45, context=ctx)
conn.request("POST", "/v2/plugin/auth/token/refresh", body=b"{}", headers=h)
r = conn.getresponse()
raw = r.read()
print("status:", r.status)
d = json.loads(raw)
nd = d.get("data", {})
if nd.get("accessToken"):
    auth["accessToken"] = nd["accessToken"]
    auth["expiresAt"] = nd.get("expiresAt") or nd.get("expiresIn")
    json.dump(S, open(r"D:\wild-work\data\codebuddy_intl_session.json", "w", encoding="utf-8"), ensure_ascii=False, indent=1)
    print("session updated, new token head:", nd["accessToken"][:40])
else:
    print("no accessToken", raw[:400])
conn.close()