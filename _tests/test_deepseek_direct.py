#!/usr/bin/env python3
"""直接 curl DeepSeek 4.1 Flash 国际版 API 的脚本"""

import requests
import json
import time

# 从认证文件读取
auth_file = "auths/codebuddy-jxylisty.json"
with open(auth_file, 'r', encoding='utf-8') as f:
    auth_data = json.load(f)

access_token = auth_data['auth']['accessToken']
uid = auth_data['account']['uid']
domain = auth_data['auth']['domain']

# 生成 trace ID
import uuid
trace_id = str(uuid.uuid4()).replace('-', '')

url = "https://www.codebuddy.ai/v2/chat/completions"

headers = {
    "Content-Type": "application/json",
    "Accept": "text/event-stream",
    "X-Requested-With": "XMLHttpRequest",
    "User-Agent": "CodeBuddyIDE/3.10.0",
    "X-Domain": domain,
    "X-Product": "SaaS",
    "X-Product-Code": "codebuddy",
    "X-IDE-Name": "CodeBuddyIDE",
    "Authorization": f"Bearer {access_token}",
    "X-User-Id": uid,
    "Trace-ID": trace_id,
    "X-Request-ID": trace_id,
    "X-Conversation-ID": trace_id,
    "X-Conversation-Request-ID": trace_id,
    "X-Conversation-Message-ID": trace_id,
    "X-Model-ID": "deepseek-v4.1-flash",
}

payload = {
    "model": "deepseek-v4.1-flash",
    "messages": [
        {"role": "system", "content": "You are a helpful assistant."},
        {"role": "user", "content": "你好，请简单介绍一下你自己。"}
    ],
    "stream": True  # 国际版必须为 true
}

print("=" * 60)
print("Testing DeepSeek 4.1 Flash International Version")
print("=" * 60)
print(f"URL: {url}")
print(f"Model: deepseek-v4.1-flash")
print(f"Stream: True")
print("=" * 60)

try:
    response = requests.post(
        url,
        headers=headers,
        json=payload,
        stream=True,
        timeout=60
    )
    
    print(f"\nHTTP Status: {response.status_code}")
    
    if response.status_code >= 400:
        print(f"Error Response: {response.text[:500]}")
    else:
        print("\nResponse Stream:")
        print("-" * 60)
        
        full_content = []
        event_count = 0
        
        for line in response.iter_lines():
            if line:
                line_str = line.decode('utf-8')
                
                if line_str.startswith('data: '):
                    data = line_str[6:]  # Remove 'data: ' prefix
                    
                    if data == '[DONE]':
                        print("\n[DONE]")
                        break
                    
                    try:
                        chunk = json.loads(data)
                        event_count += 1
                        
                        # Extract content from choices
                        if 'choices' in chunk and len(chunk['choices']) > 0:
                            delta = chunk['choices'][0].get('delta', {})
                            content = delta.get('content', '')
                            reasoning = delta.get('reasoning_content', '')
                            
                            if content:
                                full_content.append(content)
                                print(content, end='', flush=True)
                            
                            if reasoning:
                                print(f"\n[Reasoning: {reasoning}]")
                    
                    except json.JSONDecodeError:
                        print(f"\n[Raw: {data[:100]}...]")
        
        print("\n" + "-" * 60)
        print(f"\nTotal SSE events received: {event_count}")
        print(f"Total content length: {len(''.join(full_content))} characters")
        
except requests.exceptions.Timeout:
    print("\n❌ Request timed out")
except Exception as e:
    print(f"\n❌ Error: {e}")
