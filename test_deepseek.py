import requests
import json

base_url = "http://127.0.0.1:7863/v1"
api_key = "WildWorkAPI"  # 正确的 API Key

# 测试问题
tests = [
    ("简单", "你好，请做个自我介绍"),
    ("中等", "解释一下量子力学的基本原理"),
    ("代码", "写一个快速排序的 Python 实现，并添加注释"),
    ("逻辑", "如果 A>B, B>C, C>D，那么 A 和 D 的关系是什么？为什么？"),
]

for name, question in tests:
    print(f"\n{'='*60}")
    print(f"{name}测试：{question}")
    print(f"{'='*60}")
    
    try:
        response = requests.post(
            f"{base_url}/chat/completions",
            json={
                "model": "workbuddy/deepseek-v4.1-flash",
                "messages": [{"role": "user", "content": question}],
                "stream": False
            },
            headers={"Authorization": f"Bearer {api_key}"}
        )
        
        print(f"HTTP 状态：{response.status_code}")
        result = response.json()
        
        if "choices" in result and len(result["choices"]) > 0:
            content = result["choices"][0]["message"]["content"]
            print(content)
            
            # Token 用量
            if "usage" in result:
                usage = result["usage"]
                total = usage.get("prompt_tokens", 0) + usage.get("completion_tokens", 0)
                print(f"\n💰 Token 用量：输入 {usage.get('prompt_tokens', 0)} + 输出 {usage.get('completion_tokens', 0)} = {total}")
        else:
            print(f"❌ 错误：{result.get('error', {})}")
            
    except Exception as e:
        print(f"❌ 请求失败：{e}")
