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
	clients    []*genai.Client // 多 key 轮换
	clientIdx  atomic.Int64
	chatModels []string // 多模型轮换
	modelIdx   atomic.Int64
	embedModel string
	ollamaURL  string
	temp       float32
	maxTokens  int32

	// 限流
	rpmLimit int
	mu       sync.Mutex
	tokens   int
	lastTick time.Time
}

func NewClient(ctx context.Context, apiKeys []string, chatModels []string, embedModel, ollamaURL string, temp float32, maxTokens int32, rpmLimit int) (*Client, error) {
	var clients []*genai.Client
	for _, key := range apiKeys {
		if key == "" {
			continue
		}
		client, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey:  key,
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			slog.Warn("skip api key", "error", err)
			continue
		}
		clients = append(clients, client)
	}
	if len(clients) == 0 {
		return nil, fmt.Errorf("no valid API keys")
	}

	c := &Client{
		clients:    clients,
		chatModels: chatModels,
		embedModel: embedModel,
		ollamaURL:  ollamaURL,
		temp:       temp,
		maxTokens:  maxTokens,
		rpmLimit:   rpmLimit,
		tokens:     rpmLimit,
		lastTick:   time.Now(),
	}
	slog.Info("AI clients ready", "keys", len(clients), "models", len(chatModels))
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

	// 策略：对每个模型，先试所有 key；全部 429 再降到下一个模型
	var lastErr error
	for mi, model := range c.chatModels {
		for ki, client := range c.clients {
			resp, err := client.Models.GenerateContent(ctx, model, contents, cfg)
			if err != nil {
				lastErr = err
				if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "RESOURCE_EXHAUSTED") {
					slog.Warn("quota exceeded", "key", ki, "model", model)
					continue // 换下一个 key
				}
				if strings.Contains(err.Error(), "404") {
					slog.Warn("model not found, skipping", "model", model)
					break // 换下一个模型
				}
				slog.Warn("generate failed", "key", ki, "model", model, "error", err)
				continue
			}
			text := resp.Text()
			slog.Info("generated reply", "key", ki, "model", model, "model_rank", mi+1)
			return text, nil
		}
	}
	return "", fmt.Errorf("all keys and models exhausted: %w", lastErr)
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
			resp, err := c.clients[0].Models.EmbedContent(ctx, c.embedModel,
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
