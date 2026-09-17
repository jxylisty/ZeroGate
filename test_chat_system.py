import json, uuid, urllib.request, urllib.error

S = json.load(open(r"D:\wild-work\data\codebuddy_intl_session.json", encoding="utf-8"))
acc = S["account"]; auth = S["auth"]
tok = auth["accessToken"]; uid = acc["uid"]; domain = auth.get("domain", "www.codebuddy.ai")
URL = "https://www.codebuddy.ai/v2/chat/completions"
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
    "X-Model-ID": "claude-3.7-sonnet",
    "X-Product": "SaaS",
    "X-Product-Code": "codebuddy",
    "X-IDE-Name": "CodeBuddyIDE",
    "X-Requested-With": "XMLHttpRequest",
    "User-Agent": "CodeBuddyIDE/3.10.0",
}

body = {
    "model": "claude-3.7-sonnet",
    "messages": [
        {"role": "system", "content": "You are a helpful assistant."},
        {"role": "user", "content": "Say hi in one word."},
    ],
    "stream": True,
}

def run(name, b, h=hdr):
    data = json.dumps(b, separators=(",", ":")).encode()
    req = urllib.request.Request(URL, data=data, headers=h, method="POST")
    try:
        r = urllib.request.urlopen(req, timeout=60)
        print(f"### {name}: status={r.status}")
        raw = r.read(3000)
        print("  body:", raw[:1200])
    except urllib.error.HTTPError as e:
        print(f"### {name}: HTTP {e.code} traceid={dict(e.headers).get('Traceid')}")
        print("  body:", e.read(1200)[:800])
    except Exception as e:
        print(f"### {name}: EXC {type(e).__name__}: {e}")

run("stream+system", body)

b2 = dict(body); b2["stream"] = False
run("nostream+system", b2)

# also try valid intl model names
for mdl in ["claude-sonnet-4-5", "deepseek-v4-flash", "claude-3.7-sonnet"]:
    b3 = dict(body); b3["model"] = mdl
    run(f"model={mdl}", b3)