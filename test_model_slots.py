import json, uuid, http.client, ssl

S = json.load(open(r"D:\wild-work\data\codebuddy_intl_session.json", encoding="utf-8"))
auth = S["auth"]; acc = S["account"]
tok = auth["accessToken"]; uid = acc["uid"]; domain = auth.get("domain", "www.codebuddy.ai")
uuidhex = uuid.uuid4().hex

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
    "X-Model-ID": "default-model",
    "X-Product": "SaaS",
    "X-Product-Code": "codebuddy",
    "X-IDE-Name": "CodeBuddyIDE",
    "X-Requested-With": "XMLHttpRequest",
    "User-Agent": "CodeBuddyIDE/3.10.0",
    "Accept": "text/event-stream",
}
ctx = ssl.create_default_context()

def try_model(mdl):
    body = {
        "model": mdl,
        "messages": [
            {"role": "system", "content": "You are a helpful assistant."},
            {"role": "user", "content": "Reply with exactly: OK"},
        ],
        "stream": True,
    }
    h = dict(hdr); h["X-Model-ID"] = mdl
    data = json.dumps(body, separators=(",", ":")).encode()
    conn = http.client.HTTPSConnection("www.codebuddy.ai", 443, timeout=60, context=ctx)
    conn.request("POST", "/v2/chat/completions", body=data, headers=h)
    r = conn.getresponse()
    st = r.status
    raw = r.read(2500)
    conn.close()
    print(f"### model={mdl} -> {st}")
    print("   ", raw[:600].decode("utf-8", "replace"))
    print()

for mdl in ["default-model", "auto-chat", "fast-model", "balanced-model", "primary-model", "deep-model"]:
    try_model(mdl)