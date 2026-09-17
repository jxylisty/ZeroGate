import json, uuid, http.client, ssl

S = json.load(open(r"D:\wild-work\data\codebuddy_intl_session.json", encoding="utf-8"))
acc = S["account"]; auth = S["auth"]
tok = auth["accessToken"]; uid = acc["uid"]; domain = auth.get("domain", "www.codebuddy.ai")
uuidhex = uuid.uuid4().hex

body = {
    "model": "claude-3.7-sonnet",
    "messages": [
        {"role": "system", "content": "You are a helpful assistant."},
        {"role": "user", "content": "Say hi in one word."},
    ],
    "stream": True,
}
data = json.dumps(body, separators=(",", ":")).encode()

hdr = {
    "Content-Type": "application/json",
    "Authorization": "Bearer " + tok,
    "X-User-Id": uid,
    "X-Domain": domain,
    "Trace-ID": uuidhex,
    "X-Request-ID": uuidhex,
    "X-Conversation-ID": uuidhex,
    "X-Conversation-Request-ID": uuidhex,
    "X-Conversation-Message-ID": uuidhex,
    "X-Model-ID": "claude-3.7-sonnet",
    "X-Product": "SaaS",
    "X-Product-Code": "codebuddy",
    "X-IDE-Name": "CodeBuddyIDE",
    "X-Requested-With": "XMLHttpRequest",
    "User-Agent": "CodeBuddyIDE/3.10.0",
    "Accept": "text/event-stream",
    "Connection": "close",
}

ctx = ssl.create_default_context()
conn = http.client.HTTPSConnection("www.codebuddy.ai", 443, timeout=60, context=ctx)
conn.request("POST", "/v2/chat/completions", body=data, headers=hdr)
r = conn.getresponse()
print("status:", r.status, r.reason)
print("response headers:")
for k, v in r.getheaders():
    print("  ", k, ":", v)
raw = r.read(2500)
print("body head:", raw[:1000])
conn.close()