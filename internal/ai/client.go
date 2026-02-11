package ai

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/genai"
)

type Client struct {
	client    *genai.Client
	chatModel string
	embedModel string
	temp      float32
	maxTokens int32

	// 限流
	rpmLimit int
	mu       sync.Mutex
	tokens   int
	lastTick time.Time
}

func NewClient(ctx context.Context, apiKey, chatModel, embedModel string, temp float32, maxTokens int32, rpmLimit int) (*Client, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("create genai client: %w", err)
	}

	c := &Client{
		client:     client,
		chatModel:  chatModel,
		embedModel: embedModel,
		temp:       temp,
		maxTokens:  maxTokens,
		rpmLimit:   rpmLimit,
		tokens:     rpmLimit,
		lastTick:   time.Now(),
	}
	return c, nil
}

// GenerateChat 生成对话回复
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

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := c.client.Models.GenerateContent(ctx, c.chatModel, contents, cfg)
		if err != nil {
			lastErr = err
			slog.Warn("gemini generate failed, retrying", "attempt", attempt+1, "error", err)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
			continue
		}
		text := resp.Text()
		return text, nil
	}
	return "", fmt.Errorf("gemini generate failed after 3 attempts: %w", lastErr)
}

// Embed 生成文本嵌入向量
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	if err := c.waitForToken(ctx); err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := c.client.Models.EmbedContent(ctx, c.embedModel,
			[]*genai.Content{genai.NewContentFromText(text, genai.RoleUser)}, nil)
		if err != nil {
			lastErr = err
			slog.Warn("gemini embed failed, retrying", "attempt", attempt+1, "error", err)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
			continue
		}
		if len(resp.Embeddings) == 0 {
			return nil, fmt.Errorf("empty embedding response")
		}
		return resp.Embeddings[0].Values, nil
	}
	return nil, fmt.Errorf("gemini embed failed after 3 attempts: %w", lastErr)
}

// EmbedFunc 返回一个可用于 chromem-go 的 embedding 函数
func (c *Client) EmbedFunc() func(ctx context.Context, text string) ([]float32, error) {
	return c.Embed
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

	// 等到下一分钟
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
