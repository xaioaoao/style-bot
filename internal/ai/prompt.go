package ai

import (
	"fmt"
	"strings"
)

// BuildSystemPrompt 组装完整的 System Prompt
func BuildSystemPrompt(myName, targetName string, styleProfile string, relationship string, ragExamples []string) string {
	var b strings.Builder

	// 身份定义
	fmt.Fprintf(&b, "你是%s本人，正在通过 QQ 和%s聊天。\n", myName, targetName)
	b.WriteString("你必须完全模仿" + myName + "的说话风格。不要暴露你是 AI。\n")
	b.WriteString("如果被问到是不是机器人，用" + myName + "的风格否认。\n\n")

	// 风格档案
	if styleProfile != "" {
		b.WriteString("## 你的说话风格\n")
		b.WriteString(styleProfile)
		b.WriteString("\n\n")
	}

	// 关系记忆
	if relationship != "" {
		fmt.Fprintf(&b, "## 你和%s的关系\n", targetName)
		b.WriteString(relationship)
		b.WriteString("\n\n")
	}

	// RAG 示例
	if len(ragExamples) > 0 {
		b.WriteString("## 你在类似场景下的真实回复示例\n")
		for i, ex := range ragExamples {
			fmt.Fprintf(&b, "示例%d：\n%s\n\n", i+1, ex)
		}
	}

	// 规则
	b.WriteString("## 回复规则\n")
	b.WriteString("1. 严格模仿上面的风格示例来回复\n")
	b.WriteString("2. 保持消息简短\n")
	b.WriteString("3. 如果要发多条短消息，用 ||| 分隔\n")
	b.WriteString("4. 不知道的事情就含糊带过，不要编造具体细节\n")
	b.WriteString("5. 绝不使用：敬语、长段落、列表格式、\"我理解你的感受\" 等 AI 味表达\n")

	return b.String()
}

// SplitMultiMessage 按 ||| 分割多条消息
func SplitMultiMessage(reply string) []string {
	parts := strings.Split(reply, "|||")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return []string{reply}
	}
	return result
}

// FilterAIPatterns 过滤明显的 AI 味表达
func FilterAIPatterns(reply string) string {
	aiPatterns := []string{
		"作为一个AI",
		"作为AI",
		"我理解你的感受",
		"我很高兴",
		"我很抱歉",
		"如果你有任何",
		"请随时",
		"希望这对你有帮助",
		"有什么我可以帮助",
	}
	for _, p := range aiPatterns {
		reply = strings.ReplaceAll(reply, p, "")
	}
	return strings.TrimSpace(reply)
}
