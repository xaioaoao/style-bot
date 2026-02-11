package parser

import "time"

// ChatMessage 单条聊天消息
type ChatMessage struct {
	Timestamp time.Time
	Sender    string // "我" 或对方名字
	Content   string
	IsMe      bool
}

// Conversation 一段完整对话（按时间间隔切分）
type Conversation struct {
	Messages []ChatMessage
	StartAt  time.Time
	EndAt    time.Time
}

// FormatAsExample 将对话格式化为 prompt 示例文本
func (c *Conversation) FormatAsExample(myName, targetName string) string {
	var s string
	for _, m := range c.Messages {
		name := targetName
		if m.IsMe {
			name = myName
		}
		s += name + "：" + m.Content + "\n"
	}
	return s
}
