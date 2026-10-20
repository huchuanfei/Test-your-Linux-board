// Package engine 实现板卡测试核心业务：SSH连接、双模式脚本执行引擎、JSON容错解析。
package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// TestItem 板卡脚本输出的结构化测试结果项（协议字段，见需求文档第四节）
type TestItem struct {
	TestName string `json:"test_name"` // 测试名称
	TestItem string `json:"test_item"` // 测试ID
	Result   string `json:"result"`    // pass / fail
	Msg      string `json:"msg"`       // 简要信息
	RawLog   string `json:"raw_log"`   // 完整过程日志
}

const (
	ResultPass = "pass"
	ResultFail = "fail"
)

// trailingCommaRe 匹配对象/数组尾部多余逗号（AI生成脚本的常见JSON偏差）
var trailingCommaRe = regexp.MustCompile(`,\s*([}\]])`)

// ParseResults 从stdout容错解析测试结果JSON数组：
//   - 自动截取首个'['到末个']'之间的内容，剔除前后杂字符
//   - 兼容仅输出单个对象（自动包裹为数组）
//   - 兼容尾部多余逗号
//   - result统一小写规范化
func ParseResults(stdout string) ([]TestItem, error) {
	s := strings.TrimSpace(stdout)
	if s == "" {
		return nil, fmt.Errorf("stdout为空，脚本未输出测试结果")
	}

	payload, err := extractJSON(s)
	if err != nil {
		return nil, err
	}

	items, err := unmarshalItems(payload)
	if err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w", err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("JSON数组为空，无测试结果项")
	}
	for i := range items {
		items[i].Result = normalizeResult(items[i].Result)
	}
	return items, nil
}

// extractJSON 截取合法JSON负载：优先JSON数组，其次单个对象
func extractJSON(s string) (string, error) {
	start, end := strings.Index(s, "["), strings.LastIndex(s, "]")
	if start >= 0 && end > start {
		return s[start : end+1], nil
	}
	// 兼容单个对象输出：{...} 自动包裹为数组
	ob, cb := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if ob >= 0 && cb > ob {
		return "[" + s[ob:cb+1] + "]", nil
	}
	return "", fmt.Errorf("stdout中未找到JSON内容: %q", truncate(s, 120))
}

func unmarshalItems(payload string) ([]TestItem, error) {
	var items []TestItem
	if err := json.Unmarshal([]byte(payload), &items); err == nil {
		return items, nil
	}
	// 尾逗号容错重试
	fixed := trailingCommaRe.ReplaceAllString(payload, "$1")
	if err := json.Unmarshal([]byte(fixed), &items); err != nil {
		return nil, err
	}
	return items, nil
}

// normalizeResult result字段规范化：去空白、转小写，映射常见同义词
func normalizeResult(r string) string {
	r = strings.ToLower(strings.TrimSpace(r))
	switch r {
	case "success", "ok", "true":
		return ResultPass
	case "failed", "error", "false":
		return ResultFail
	}
	return r
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
