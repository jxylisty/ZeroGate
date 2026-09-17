package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func main() {
	apiKey := os.Getenv("ZERO_GATE_API_KEY")
	if apiKey == "" {
		apiKey = "sk-test" // 默认测试 key
	}

	baseURL := "http://127.0.0.1:7863/v1"

	// 测试问题列表
	tests := []struct {
		name string
		msg  string
	}{
		{"简单", "你好，请做个自我介绍"},
		{"中等", "解释一下量子力学的基本原理"},
		{"代码", "写一个快速排序的 Python 实现，并添加注释"},
		{"逻辑", "如果 A>B, B>C, C>D，那么 A 和 D 的关系是什么？为什么？"},
		{"复杂", "分析当前人工智能发展的主要瓶颈和未来趋势，给出详细论证"},
	}

	for _, t := range tests {
		fmt.Printf("\n%s测试：%s\n", t.name, t.msg)
		fmt.Println(strings.Repeat("=", 50))

		reqBody := map[string]any{
			"model": "workbuddy/deepseek-v4.1-flash",
			"messages": []map[string]string{
				{"role": "user", "content": t.msg},
			},
			"stream": false,
		}

		resp, err := http.Post(baseURL+"/chat/completions", "application/json", 
			jsonReader(reqBody))
		if err != nil {
			fmt.Printf("❌ 请求失败：%v\n", err)
			continue
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("❌ 读取响应失败：%v\n", err)
			continue
		}
		
		fmt.Printf("HTTP 状态：%d\n", resp.StatusCode)
		fmt.Printf("响应：%s\n", string(body))
		
		var result map[string]any
		json.Unmarshal(body, &result)

		if choices, ok := result["choices"].([]any); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]any); ok {
				if msg, ok := choice["message"].(map[string]any); ok {
					if content, ok := msg["content"].(string); ok {
						fmt.Println(content)
					}
				}
			}
		}

		// 显示 token 用量
		if usage, ok := result["usage"].(map[string]any); ok {
			if prompt, ok := usage["prompt_tokens"].(float64); ok {
				if completion, ok := usage["completion_tokens"].(float64); ok {
					fmt.Printf("\n💰 Token 用量：输入 %.0f + 输出 %.0f = %.0f\n", 
						prompt, completion, prompt+completion)
				}
			}
		}
	}
}

func jsonReader(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}
