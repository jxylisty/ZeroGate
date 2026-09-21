#!/usr/bin/env python3
"""测试 DeepSeek 4.1 Flash 的代码能力"""

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

# 测试代码生成问题
payload = {
    "model": "deepseek-v4.1-flash",
    "messages": [
        {"role": "system", "content": "You are a helpful assistant."},
        {"role": "user", "content": """写一个 Python 函数，实现快速排序算法，并添加详细注释。
要求：
1. 函数接收一个整数列表作为输入
2. 返回排序后的新列表（不修改原列表）
3. 使用递归实现
4. 添加时间复杂度分析"""
        }
    ],
    "stream": True
}

print("=" * 60)
print("Testing DeepSeek 4.1 Flash - Code Generation")
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
        print("\nResponse:")
        print("-" * 60)
        
        full_content = []
        event_count = 0
        
        for line in response.iter_lines():
            if line:
                line_str = line.decode('utf-8')
                
                if line_str.startswith('data: '):
                    data = line_str[6:]
                    
                    if data == '[DONE]':
                        break
                    
                    try:
                        chunk = json.loads(data)
                        event_count += 1
                        
                        if 'choices' in chunk and len(chunk['choices']) > 0:
                            delta = chunk['choices'][0].get('delta', {})
                            content = delta.get('content', '')
                            
                            if content:
                                full_content.append(content)
                                print(content, end='', flush=True)
                    
                    except json.JSONDecodeError:
                        pass
        
        print("\n" + "-" * 60)
        print(f"\nTotal SSE events: {event_count}")
        print(f"Content length: {len(''.join(full_content))} characters")
        
except Exception as e:
    print(f"\nError: {e}")
