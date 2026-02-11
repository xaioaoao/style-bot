package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/genai"
)

type Message struct {
	Role      string    `json:"role"`    // "user" / "model"
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type Session struct {
	Messages   []Message `json:"messages"`
	LastActive time.Time `json:"last_active"`
}

type Manager struct {
	mu          sync.Mutex
	session     *Session
	maxTurns    int
	sessionDir  string
	sessionFile string
}

func NewManager(maxTurns int, sessionDir string) (*Manager, error) {
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}

	m := &Manager{
		maxTurns:    maxTurns,
		sessionDir:  sessionDir,
		sessionFile: filepath.Join(sessionDir, "session.json"),
	}

	// 尝试从文件恢复
	if data, err := os.ReadFile(m.sessionFile); err == nil {
		var s Session
		if json.Unmarshal(data, &s) == nil {
			m.session = &s
		}
	}
	if m.session == nil {
		m.session = &Session{LastActive: time.Now()}
	}
	return m, nil
}

// AddUserMessage 添加对方发来的消息
func (m *Manager) AddUserMessage(content string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.session.Messages = append(m.session.Messages, Message{
		Role:      "user",
		Content:   content,
		Timestamp: time.Now(),
	})
	m.session.LastActive = time.Now()
	m.trim()
}

// AddBotReply 添加 bot 的回复
func (m *Manager) AddBotReply(content string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.session.Messages = append(m.session.Messages, Message{
		Role:      "model",
		Content:   content,
		Timestamp: time.Now(),
	})
	m.trim()
}

// GetHistory 获取对话历史，转换为 genai.Content 格式
func (m *Manager) GetHistory() []*genai.Content {
	m.mu.Lock()
	defer m.mu.Unlock()

	contents := make([]*genai.Content, 0, len(m.session.Messages))
	for _, msg := range m.session.Messages {
		var role genai.Role = genai.RoleUser
		if msg.Role == "model" {
			role = genai.RoleModel
		}
		contents = append(contents, genai.NewContentFromText(msg.Content, role))
	}
	return contents
}

// Save 持久化到文件
func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := json.MarshalIndent(m.session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	return os.WriteFile(m.sessionFile, data, 0644)
}

func (m *Manager) trim() {
	// 保留最近 maxTurns*2 条消息（每轮 = 1 user + 1 model）
	max := m.maxTurns * 2
	if len(m.session.Messages) > max {
		m.session.Messages = m.session.Messages[len(m.session.Messages)-max:]
	}
}
