#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
ZeroGate · QQ Mail IMAP 极速验证码侦测与自动保存服务
1. 监听 zzx051012@qq.com，抓取 Atria 6 位验证码 (GET /api/code)
2. 接收控制台全自动抓取的 Key 并直接写入 src-true/auths/atria-*.json (POST /api/save-key)
"""

import sys
try:
    sys.stdout.reconfigure(encoding='utf-8', errors='replace')
except Exception:
    pass

import os
import time
import re
import json
import imaplib
import email
import email.header
import threading
from http.server import HTTPServer, BaseHTTPRequestHandler

QQ_EMAIL = "zzx051012@qq.com"
QQ_AUTH_CODE = "zgqfqabebjcsdecf"
IMAP_SERVER = "imap.qq.com"
IMAP_PORT = 993
HTTP_PORT = 7864
AUTHS_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "auths"))

state_lock = threading.Lock()
latest_code_data = {
    "code": "",
    "subject": "",
    "sender": "",
    "timestamp": 0,
    "age_seconds": 9999,
    "is_used": False
}

def decode_mime_words(s):
    if not s:
        return ""
    try:
        parts = email.header.decode_header(s)
        decoded = []
        for content, encoding in parts:
            if isinstance(content, bytes):
                decoded.append(content.decode(encoding or "utf-8", errors="ignore"))
            else:
                decoded.append(content)
        return "".join(decoded)
    except Exception:
        return str(s)

def extract_code_from_text(text):
    if not text:
        return None
    patterns = [
        r'verification\s+code\s+(?:is\s+)?[:：]?\s*(\d{6})\b',
        r'验证码(?:为|是|：|:)?\s*(\d{6})\b',
        r'\bcode\s+is\s+(\d{6})\b',
        r'\b(\d{6})\b'
    ]
    for p in patterns:
        m = re.search(p, text, re.IGNORECASE)
        if m:
            return m.group(1)
    return None

def imap_worker():
    global latest_code_data
    print(f"[*] QQ Mail IMAP 监听启动: {QQ_EMAIL}", flush=True)
    
    while True:
        mail = None
        try:
            mail = imaplib.IMAP4_SSL(IMAP_SERVER, IMAP_PORT)
            mail.login(QQ_EMAIL, QQ_AUTH_CODE)
            print("[+] IMAP 连接成功，正在监听邮件...", flush=True)

            while True:
                try:
                    mail.select("INBOX")
                    status, data = mail.search(None, "ALL")
                    if status != "OK" or not data or not data[0]:
                        time.sleep(1.5)
                        continue

                    ids = data[0].split()
                    recent_ids = ids[-5:]

                    found_code = None
                    found_subject = ""
                    found_sender = ""

                    for eid in reversed(recent_ids):
                        s, d = mail.fetch(eid, "(RFC822)")
                        if s != "OK" or not d:
                            continue

                        raw_email = d[0][1]
                        msg = email.message_from_bytes(raw_email)
                        subj = decode_mime_words(msg.get("Subject", ""))
                        sender = decode_mime_words(msg.get("From", ""))

                        if "atria" in sender.lower() or "atria" in subj.lower() or "verification" in subj.lower() or "验证码" in subj:
                            body_text = ""
                            for part in msg.walk():
                                if part.get_content_type() in ["text/plain", "text/html"]:
                                    payload = part.get_payload(decode=True)
                                    if payload:
                                        body_text += payload.decode("utf-8", errors="ignore") + "\n"

                            code = extract_code_from_text(body_text) or extract_code_from_text(subj)
                            if code:
                                found_code = code
                                found_subject = subj
                                found_sender = sender
                                break

                    if found_code:
                        with state_lock:
                            if latest_code_data["code"] != found_code:
                                print(f"[+] [CAPTURED] 捕获到新验证码: {found_code} | 来自: {found_sender}", flush=True)
                                latest_code_data["code"] = found_code
                                latest_code_data["subject"] = found_subject
                                latest_code_data["sender"] = found_sender
                                latest_code_data["timestamp"] = time.time()
                                latest_code_data["is_used"] = False

                except Exception as inner_e:
                    print(f"[-] 轮询异常: {inner_e}", flush=True)
                    break

                time.sleep(1.2)

        except Exception as e:
            print(f"[-] IMAP 连接异常: {e}，5秒后重试...", flush=True)
            time.sleep(5)
        finally:
            if mail:
                try:
                    mail.logout()
                except Exception:
                    pass

class CodeHTTPHandler(BaseHTTPRequestHandler):
    def _send_cors_headers(self):
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "*")

    def do_OPTIONS(self):
        self.send_response(200)
        self._send_cors_headers()
        self.end_headers()

    def do_GET(self):
        if self.path.startswith("/api/code"):
            with state_lock:
                now = time.time()
                age = round(now - latest_code_data["timestamp"], 1) if latest_code_data["timestamp"] > 0 else 9999
                resp = {
                    "status": "ok",
                    "code": latest_code_data["code"],
                    "subject": latest_code_data["subject"],
                    "sender": latest_code_data["sender"],
                    "timestamp": latest_code_data["timestamp"],
                    "age_seconds": age,
                    "is_used": latest_code_data["is_used"]
                }

            self.send_response(200)
            self._send_cors_headers()
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.end_headers()
            self.wfile.write(json.dumps(resp, ensure_ascii=False).encode("utf-8"))

        elif self.path.startswith("/api/mark-used"):
            with state_lock:
                latest_code_data["is_used"] = True
            self.send_response(200)
            self._send_cors_headers()
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.end_headers()
            self.wfile.write(b'{"status": "ok", "message": "marked used"}')

        elif self.path.startswith("/health"):
            self.send_response(200)
            self._send_cors_headers()
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.end_headers()
            self.wfile.write(json.dumps({"status": "healthy", "service": "mail_code_server", "port": HTTP_PORT}).encode("utf-8"))

        else:
            self.send_response(404)
            self._send_cors_headers()
            self.end_headers()
            self.wfile.write(b"Not Found")

    def do_POST(self):
        if self.path.startswith("/api/save-key"):
            try:
                length = int(self.headers.get("Content-Length", 0))
                body = self.rfile.read(length).decode("utf-8") if length > 0 else "{}"
                data = json.loads(body)
                api_key = data.get("apiKey", "").strip()
                email_addr = data.get("email", "").strip()
                
                if not api_key:
                    self.send_response(400)
                    self._send_cors_headers()
                    self.end_headers()
                    self.wfile.write(b'{"status": "error", "message": "missing apiKey"}')
                    return

                # 检查是否已存在相同的 apiKey，避免重复创建
                for f in os.listdir(AUTHS_DIR):
                    if f.startswith("atria-") and f.endswith(".json"):
                        try:
                            with open(os.path.join(AUTHS_DIR, f), "r", encoding="utf-8") as fp:
                                c = json.load(fp)
                                if c.get("apiKey") == api_key:
                                    print(f"[=] Key 已存在于 {f}，无需重复保存", flush=True)
                                    self.send_response(200)
                                    self._send_cors_headers()
                                    self.send_header("Content-Type", "application/json; charset=utf-8")
                                    self.end_headers()
                                    self.wfile.write(json.dumps({"status": "ok", "filename": f, "exists": True}).encode("utf-8"))
                                    return
                        except Exception:
                            pass

                # 生成唯一文件名（使用 key 后 8 位后缀，确保绝对不会相互覆盖）
                key_suffix = re.sub(r'[^a-zA-Z0-9]', '', api_key)[-8:].lower()
                id_part = email_addr.split("@")[0] if email_addr else "user"
                id_part = re.sub(r'[^a-zA-Z0-9_-]', '', id_part)
                filename = f"atria-{id_part}-{key_suffix}.json"
                filepath = os.path.join(AUTHS_DIR, filename)

                cfg = {
                    "apiKey": api_key,
                    "uid": f"atria-{id_part}-{key_suffix}",
                    "nickname": f"Atria-{id_part}"
                }

                os.makedirs(AUTHS_DIR, exist_ok=True)
                with open(filepath, "w", encoding="utf-8") as f:
                    json.dump(cfg, f, indent=2, ensure_ascii=False)

                print(f"[+] [SAVED] 成功保存 Atria Key: {filename} -> {filepath}", flush=True)

                self.send_response(200)
                self._send_cors_headers()
                self.send_header("Content-Type", "application/json; charset=utf-8")
                self.end_headers()
                self.wfile.write(json.dumps({
                    "status": "ok",
                    "filename": filename,
                    "path": filepath,
                    "apiKey": api_key[:10] + "..."
                }).encode("utf-8"))
            except Exception as e:
                print(f"[-] 保存 Key 失败: {e}", flush=True)
                self.send_response(500)
                self._send_cors_headers()
                self.end_headers()
                self.wfile.write(json.dumps({"status": "error", "error": str(e)}).encode("utf-8"))
        else:
            self.send_response(404)
            self._send_cors_headers()
            self.end_headers()
            self.wfile.write(b"Not Found")

    def log_message(self, format, *args):
        pass

def run_server():
    t = threading.Thread(target=imap_worker, daemon=True)
    t.start()

    server_address = ("127.0.0.1", HTTP_PORT)
    httpd = HTTPServer(server_address, CodeHTTPHandler)
    print(f"[+] 本地 API 服务就绪: http://127.0.0.1:{HTTP_PORT} (抓码/自动存Key)", flush=True)
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\n[*] 服务停止", flush=True)

if __name__ == "__main__":
    run_server()
