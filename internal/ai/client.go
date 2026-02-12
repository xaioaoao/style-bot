package ai

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	chromem "github.com/philippgille/chromem-go"
	"google.golang.org/genai"
)

type Client struct {
	client     *genai.Client
	chatModels []string // 多模型轮换
	modelIdx   atomic.Int64
	embedModel string
	ollamaURL  string // 本地 Ollama URL，非空时 embedding 走 Ollama
	temp       float32
	maxTokens  int32

	// 限流
	rpmLimit int
	mu       sync.Mutex
	tokens   int
	lastTick time.Time
}

func NewClient(ctx context.Context, apiKey string, chatModels []string, embedModel, ollamaURL string, temp float32, maxTokens int32, rpmLimit int) (*Client, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("create genai client: %w", err)
	}

	c := &Client{
		client:     client,
		chatModels: chatModels,
		embedModel: embedModel,
		ollamaURL:  ollamaURL,
		temp:       temp,
		maxTokens:  maxTokens,
		rpmLimit:   rpmLimit,
		tokens:     rpmLimit,
		lastTick:   time.Now(),
	}
	return c, nil
}

// currentModel 获取当前模型
func (c *Client) currentModel() string {
	idx := c.modelIdx.Load() % int64(len(c.chatModels))
	return c.chatModels[idx]
}

// rotateModel 切换到下一个模型
func (c *Client) rotateModel() string {
	newIdx := c.modelIdx.Add(1) % int64(len(c.chatModels))
	model := c.chatModels[newIdx]
	slog.Info("rotating to next model", "model", model)
	return model
}

// GenerateChat 生成对话回复，429 时自动切换模型
func (c *Client) GenerateChat(ctx context.Context, systemPrompt string, history []*genai.Content, userMsg string) (string, error) {
	if err := c.waitForToken(ctx); err != nil {
		return "", err
	}

	contents := make([]*genai.Content, 0, len(history)+1)
	contents = append(contents, history...)
	contents = append(contents, genai.NewContentFromText(userMsg, genai.RoleUser))

	cfg := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(systemPrompt, genai.RoleUser),
		Temperature:       genai.Ptr(c.temp),
		MaxOutputTokens:   c.maxTokens,
	}

	// 尝试所有模型，每个模型最多重试 2 次
	totalAttempts := len(c.chatModels) * 2
	var lastErr error
	for attempt := 0; attempt < totalAttempts; attempt++ {
		model := c.currentModel()
		resp, err := c.client.Models.GenerateContent(ctx, model, contents, cfg)
		if err != nil {
			lastErr = err
			if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "RESOURCE_EXHAUSTED") {
				slog.Warn("model quota exceeded, switching", "model", model, "attempt", attempt+1)
				c.rotateModel()
				time.Sleep(time.Second)
				continue
			}
			slog.Warn("generate failed, retrying", "model", model, "attempt", attempt+1, "error", err)
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			continue
		}
		text := resp.Text()
		slog.Debug("generated reply", "model", model)
		return text, nil
	}
	return "", fmt.Errorf("all models exhausted after %d attempts: %w", totalAttempts, lastErr)
}

// EmbedFunc 返回一个可用于 chromem-go 的 embedding 函数
// 优先使用 Ollama（本地，免费无限），回退到 Gemini API
func (c *Client) EmbedFunc() chromem.EmbeddingFunc {
	if c.ollamaURL != "" {
		slog.Info("using Ollama for embedding", "model", c.embedModel, "url", c.ollamaURL)
		return chromem.NewEmbeddingFuncOllama(c.embedModel, c.ollamaURL)
	}
	slog.Info("using Gemini API for embedding", "model", c.embedModel)
	return func(ctx context.Context, text string) ([]float32, error) {
		var lastErr error
		for attempt := 0; attempt < 3; attempt++ {
			resp, err := c.client.Models.EmbedContent(ctx, c.embedModel,
				[]*genai.Content{genai.NewContentFromText(text, genai.RoleUser)}, nil)
			if err != nil {
				lastErr = err
				slog.Warn("embed failed, retrying", "attempt", attempt+1, "error", err)
				time.Sleep(time.Duration(1<<attempt) * time.Second)
				continue
			}
			if len(resp.Embeddings) == 0 {
				return nil, fmt.Errorf("empty embedding response")
			}
			return resp.Embeddings[0].Values, nil
		}
		return nil, fmt.Errorf("embed failed after 3 attempts: %w", lastErr)
	}
}

// waitForToken 简单令牌桶限流
func (c *Client) waitForToken(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(c.lastTick)
	if elapsed >= time.Minute {
		c.tokens = c.rpmLimit
		c.lastTick = now
	}

	if c.tokens > 0 {
		c.tokens--
		return nil
	}

	wait := time.Minute - elapsed
	c.mu.Unlock()
	slog.Info("rate limit reached, waiting", "duration", wait)
	select {
	case <-ctx.Done():
		c.mu.Lock()
		return ctx.Err()
	case <-time.After(wait):
	}
	c.mu.Lock()
	c.tokens = c.rpmLimit - 1
	c.lastTick = time.Now()
	return nil
}
