package parser

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/pbkdf2"
)

// jsonlEntry 表示 JSONL 中的一行
type jsonlEntry struct {
	Messages []jsonlMessage `json:"messages"`
}

type jsonlMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// DecryptFile 解密 AES-256-GCM 加密的文件
// 文件格式: salt(16) + nonce(16) + tag(16) + ciphertext
func DecryptFile(path string, password string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	if len(data) < 48 {
		return nil, fmt.Errorf("file too small")
	}

	salt := data[:16]
	nonce := data[16:32]
	tag := data[32:48]
	ciphertext := data[48:]

	key := pbkdf2.Key([]byte(password), salt, 100000, 32, sha256.New)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}

	gcm, err := cipher.NewGCMWithNonceSize(block, 16)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}

	// GCM 的 decrypt 需要 ciphertext+tag 拼在一起
	ciphertextWithTag := append(ciphertext, tag...)
	plaintext, err := gcm.Open(nil, nonce, ciphertextWithTag, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	return plaintext, nil
}

// ParseJSONLBytes 解析 JSONL 格式的聊天记录（从解密后的字节）
// userRole: "user" 在 JSONL 中对应的身份（传 true 表示 user=我）
func ParseJSONLBytes(data []byte, myName string, targetName string, userIsMe bool) ([]ChatMessage, error) {
	var allMessages []ChatMessage

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	lineNum := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry jsonlEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		for _, msg := range entry.Messages {
			isMe := (msg.Role == "user" && userIsMe) || (msg.Role == "assistant" && !userIsMe)

			sender := targetName
			if isMe {
				sender = myName
			}

			// 每条 content 可能包含多条消息（\n 分隔）
			parts := strings.Split(msg.Content, "\n")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}

				allMessages = append(allMessages, ChatMessage{
					Timestamp: time.Time{}, // JSONL 中没有时间戳
					Sender:    sender,
					Content:   part,
					IsMe:      isMe,
				})
			}
		}
		lineNum++
	}

	return allMessages, nil
}

// ParseJSONLToConversations 直接将 JSONL 解析为对话片段（更适合这个格式）
// 每行 JSONL 天然就是一组对话
func ParseJSONLToConversations(data []byte, myName string, targetName string, userIsMe bool) ([]Conversation, error) {
	var conversations []Conversation

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry jsonlEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		var conv Conversation
		for _, msg := range entry.Messages {
			isMe := (msg.Role == "user" && userIsMe) || (msg.Role == "assistant" && !userIsMe)

			sender := targetName
			if isMe {
				sender = myName
			}

			conv.Messages = append(conv.Messages, ChatMessage{
				Sender:  sender,
				Content: msg.Content,
				IsMe:    isMe,
			})
		}

		if len(conv.Messages) >= 2 {
			conversations = append(conversations, conv)
		}
	}

	return conversations, nil
}
