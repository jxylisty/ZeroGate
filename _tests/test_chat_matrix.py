import json, uuid, ssl, urllib.request, urllib.error, sys, os

S = json.load(open(r"D:\wild-work\data\codebuddy_intl_session.json", encoding="utf-8"))
acc = S["account"]; auth = S["auth"]
tok = auth["accessToken"]; uid = acc["uid"]
domain = auth.get("domain", "www.codebuddy.ai")
print("uid:", uid, "domain:", domain, "token head:", tok[:30])

URL = "https://www.codebuddy.ai/v2/chat/completions"
uuidhex = uuid.uuid4().hex

def build_headers(variant):
    h = {
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
        "User-Agent": "CodeBuddyIDE/3.10.0",
    }
    if variant == "minimal":
        pass
    elif variant == "empty_ent":
        h["X-Enterprise-Id"] = ""
        h["X-Tenant-Id"] = ""
    elif variant == "product":
        h["X-Product"] = "SaaS"
        h["X-Product-Code"] = "codebuddy"
        h["X-IDE-Name"] = "CodeBuddyIDE"
        h["X-IDE-Version"] = "3.10.0"
        h["X-Requested-With"] = "XMLHttpRequest"
    elif variant == "nodomain":
        del h["X-Domain"]
    elif variant == "nostream_body":
        pass  # body handles
    return h

BODIES = {
    "stream": {"model": "claude-3.7-sonnet", "messages": [{"role": "user", "content": "hi"}], "stream": True},
    "nostream": {"model": "claude-3.7-sonnet", "messages": [{"role": "user", "content": "hi"}]},
}

def run(name, hdrs, body):
    data = json.dumps(body, separators=(",", ":")).encode()
    req = urllib.request.Request(URL, data=data, headers=hdrs, method="POST")
    try:
        r = urllib.request.urlopen(req, timeout=45)
        st = r.status
        head = dict(r.headers)
        b = r.read(1500)
        print(f"\n### {name}: status={st}")
        print("  headers:", json.dumps(head, ensure_ascii=False)[:600])
        print("  body:", b[:400])
    except urllib.error.HTTPError as e:
        print(f"\n### {name}: HTTP {e.code}")
        print("  headers:", json.dumps(dict(e.headers), ensure_ascii=False)[:600])
        b = e.read(1500)
        print("  body:", b[:400])
    except Exception as e:
        print(f"\n### {name}: EXC {type(e).__name__}: {e}")

run("minimal + stream", build_headers("minimal"), BODIES["stream"])
run("minimal + nostream", build_headers("minimal"), BODIES["nostream"])
run("empty_ent + stream", build_headers("empty_ent"), BODIES["stream"])
run("product + stream", build_headers("product"), BODIES["stream"])
run("nodomain + stream", build_headers("nodomain"), BODIES["stream"])