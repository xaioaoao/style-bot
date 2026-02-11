package parser

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// ParseHTMLFile 解析 WechatExporter 导出的 HTML 格式文件
// WechatExporter 的 HTML 结构可能因版本不同有差异，这里处理常见格式
func ParseHTMLFile(path string, myName string) ([]ChatMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	doc, err := goquery.NewDocumentFromReader(f)
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}

	var messages []ChatMessage

	// 尝试多种常见的 CSS 选择器
	doc.Find(".message, .msg, div[class*='message']").Each(func(i int, s *goquery.Selection) {
		// 判断发送方
		class, _ := s.Attr("class")
		isRight := strings.Contains(class, "right") || strings.Contains(class, "mine") || strings.Contains(class, "self")

		// 提取文本内容
		content := ""
		s.Find(".bubble, .content, .text, .msg-text").Each(func(j int, cs *goquery.Selection) {
			content = strings.TrimSpace(cs.Text())
		})
		if content == "" {
			content = strings.TrimSpace(s.Find("div").Last().Text())
		}
		if content == "" {
			return
		}

		// 提取昵称
		sender := ""
		s.Find(".nickname, .name, .sender").Each(func(j int, ns *goquery.Selection) {
			sender = strings.TrimSpace(ns.Text())
		})

		// 提取时间
		timeStr := ""
		s.Find(".time, .timestamp, .date").Each(func(j int, ts *goquery.Selection) {
			timeStr = strings.TrimSpace(ts.Text())
		})

		ts, _ := parseTimestamp(timeStr)

		isMe := isRight
		if sender != "" {
			isMe = isRight || sender == myName || sender == "我"
		}

		if !isMe && sender == "" {
			sender = "对方"
		} else if isMe {
			sender = myName
		}

		messages = append(messages, ChatMessage{
			Timestamp: ts,
			Sender:    sender,
			Content:   content,
			IsMe:      isMe,
		})
	})

	return messages, nil
}

// SplitConversations 按时间间隔切分对话片段
func SplitConversations(messages []ChatMessage, gapMinutes int) []Conversation {
	if len(messages) == 0 {
		return nil
	}

	gap := time.Duration(gapMinutes) * time.Minute
	var conversations []Conversation
	var current Conversation
	current.StartAt = messages[0].Timestamp

	for i, msg := range messages {
		if i > 0 && !msg.Timestamp.IsZero() && !messages[i-1].Timestamp.IsZero() {
			if msg.Timestamp.Sub(messages[i-1].Timestamp) > gap {
				// 开始新对话
				current.EndAt = messages[i-1].Timestamp
				if len(current.Messages) >= 2 { // 至少2条消息才算对话
					conversations = append(conversations, current)
				}
				current = Conversation{StartAt: msg.Timestamp}
			}
		}
		current.Messages = append(current.Messages, msg)
	}

	// 最后一段
	if len(current.Messages) >= 2 {
		current.EndAt = current.Messages[len(current.Messages)-1].Timestamp
		conversations = append(conversations, current)
	}

	return conversations
}

// FilterTextOnly 过滤非文本消息
func FilterTextOnly(messages []ChatMessage) []ChatMessage {
	nonTextPatterns := []string{
		"[图片]", "[语音]", "[视频]", "[动画表情]",
		"[文件]", "[位置]", "[链接]", "[名片]",
		"[Photo]", "[Voice]", "[Video]", "[Sticker]",
		"<img", "<video", "<audio",
	}

	var filtered []ChatMessage
	for _, m := range messages {
		skip := false
		for _, p := range nonTextPatterns {
			if strings.Contains(m.Content, p) {
				skip = true
				break
			}
		}
		if !skip && len(m.Content) > 0 {
			filtered = append(filtered, m)
		}
	}
	return filtered
}
