package parser

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// 匹配时间戳行: "2024-01-15 18:30:00 张三" 或 "2024-01-15 18:30 张三"
var headerRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}(?::\d{2})?)\s+(.+?)\s*$`)

// ParseTextFile 解析 WechatExporter 导出的 Text 格式文件
func ParseTextFile(path string, myName string) ([]ChatMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	var messages []ChatMessage
	var current *ChatMessage
	var contentBuf strings.Builder

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB buffer

	for scanner.Scan() {
		line := scanner.Text()

		if matches := headerRe.FindStringSubmatch(line); matches != nil {
			// 保存前一条消息
			if current != nil {
				current.Content = strings.TrimSpace(contentBuf.String())
				if current.Content != "" {
					messages = append(messages, *current)
				}
			}

			ts, err := parseTimestamp(matches[1])
			if err != nil {
				continue
			}
			sender := matches[2]

			current = &ChatMessage{
				Timestamp: ts,
				Sender:    sender,
				IsMe:      isMe(sender, myName),
			}
			contentBuf.Reset()
			continue
		}

		// 内容行
		if current != nil {
			if contentBuf.Len() > 0 {
				contentBuf.WriteString("\n")
			}
			contentBuf.WriteString(line)
		}
	}

	// 保存最后一条
	if current != nil {
		current.Content = strings.TrimSpace(contentBuf.String())
		if current.Content != "" {
			messages = append(messages, *current)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan file: %w", err)
	}

	return messages, nil
}

func parseTimestamp(s string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown timestamp format: %s", s)
}

func isMe(sender, myName string) bool {
	return sender == myName || sender == "我"
}
