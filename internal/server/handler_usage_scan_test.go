package server

// tokenTrackingReader 的 usage 扫描测试：覆盖三种流格式，
// 重点回归 2026-09-19 修复的 qoder 嵌套封装（usage 藏在转义的内层 JSON）。

import "testing"

func TestTrackerOpenAIPlainUsage(t *testing.T) {
	tr := &tokenTrackingReader{}
	tr.parseLine([]byte(`data: {"id":"x","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	p, c, cr, cw, ok := tr.ScanResult()
	if !ok || p != 10 || c != 5 || cr != 0 || cw != 0 {
		t.Fatalf("plain usage: got p=%d c=%d cr=%d cw=%d ok=%v", p, c, cr, cw, ok)
	}
}

func TestTrackerQoderNestedEnvelope(t *testing.T) {
	tr := &tokenTrackingReader{}
	// 原始行：usage 在转义的内层 JSON 字符串里，且 usage 自带嵌套 details 对象
	line := []byte(`data: {"body":"{\"choices\":[],\"usage\":{\"billable\":false,\"completion_tokens\":6,\"completion_tokens_details\":{},\"credits\":0,\"prompt_tokens\":22,\"prompt_tokens_details\":{\"cached_tokens\":0},\"total_tokens\":28}}"}`)
	tr.parseLine(line)
	p, c, _, _, ok := tr.ScanResult()
	if !ok || p != 22 || c != 6 {
		t.Fatalf("qoder envelope: got p=%d c=%d ok=%v（转义内层 usage 未被提取）", p, c, ok)
	}
}

func TestTrackerSoloNativeUsage(t *testing.T) {
	tr := &tokenTrackingReader{}
	tr.parseLine([]byte(`event:token_usage`))
	tr.parseLine([]byte(`data: {"prompt_tokens":100,"completion_tokens":20,"prompt_cache_hit_tokens":80}`))
	p, c, cr, _, ok := tr.ScanResult()
	if !ok || p != 100 || c != 20 || cr != 80 {
		t.Fatalf("solo usage: got p=%d c=%d cr=%d ok=%v", p, c, cr, ok)
	}
}

func TestTrackerIgnoresNonUsageLines(t *testing.T) {
	tr := &tokenTrackingReader{}
	tr.parseLine([]byte(`data: {"choices":[{"delta":{"content":"hi"}}]}`))
	tr.parseLine([]byte(`data: [DONE]`))
	_, _, _, _, ok := tr.ScanResult()
	if ok {
		t.Fatalf("non-usage lines must not set has=true")
	}
}
