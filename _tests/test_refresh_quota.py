import json, http.client, ssl, datetime

S = json.load(open(r"D:\wild-work\data\codebuddy_intl_session.json", encoding="utf-8"))
auth = S["auth"]; acc = S["account"]
tok = auth["accessToken"]; rt = auth["refreshToken"]; uid = acc["uid"]; domain = auth.get("domain", "www.codebuddy.ai")

ctx = ssl.create_default_context()

def req(method, path, body=None, headers=None, host="www.codebuddy.ai", timeout=45):
    h = {
        "Authorization": "Bearer " + tok,
        "X-User-Id": uid,
        "X-Domain": domain,
        "X-Product": "SaaS",
        "X-Product-Code": "codebuddy",
        "X-IDE-Name": "CodeBuddyIDE",
        "X-Requested-With": "XMLHttpRequest",
        "User-Agent": "CodeBuddyIDE/3.10.0",
        "Accept": "application/json",
        "Content-Type": "application/json",
    }
    if headers:
        h.update(headers)
    data = json.dumps(body, separators=(",", ":")).encode() if body is not None else None
    conn = http.client.HTTPSConnection(host, 443, timeout=timeout, context=ctx)
    try:
        conn.request(method, path, body=data, headers=h)
        r = conn.getresponse()
        raw = r.read(2500)
        print(f"### {method} {path} -> {r.status}")
        print("   ", raw.decode("utf-8", "replace")[:900])
        return r.status, raw
    finally:
        conn.close()

# 1) refresh token protocol
req("POST", "/v2/plugin/auth/token/refresh", {}, headers={
    "X-Refresh-Token": rt,
    "X-Auth-Refresh-Source": "plugin",
})
# 2) billing / quota endpoints
req("GET", "/v2/billing/meter/get-free-user-upgrade")
req("GET", "/v2/billing/meter/get-payment-type")
# 3) subscription/quota
for p in ["/v2/billing/credits", "/v2/credits", "/v2/tokens/balance", "/v2/billing/balance"]:
    req("GET", p)
print("done", datetime.datetime.now())