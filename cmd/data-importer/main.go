package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/philippgille/chromem-go"
	"google.golang.org/genai"

	"github.com/liao/style-bot/internal/parser"
	"github.com/liao/style-bot/internal/persona"
)

func main() {
	inputFile := flag.String("input", "", "chat history file (encrypted .enc or plain .jsonl/.txt/.html)")
	outputDir := flag.String("output", "./data", "output directory")
	myName := flag.String("me", "我", "my display name in chat history")
	targetName := flag.String("target", "", "target person's display name")
	apiKey := flag.String("api-key", "", "Gemini API key (or set GEMINI_API_KEY env)")
	format := flag.String("format", "auto", "input format: enc-jsonl, jsonl, text, html, auto")
	decryptKey := flag.String("decrypt-key", "", "decryption password for .enc files (from env DECRYPT_KEY if not set)")
	userIsMe := flag.Bool("user-is-me", true, "in JSONL, role=user is me (default true)")
	apiKey2 := flag.String("api-key2", "", "second Gemini API key for rotation (or GEMINI_API_KEY2 env)")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))

	if *inputFile == "" || *targetName == "" {
		fmt.Fprintf(os.Stderr, "Usage: data-importer -input <file> -target <name> [-me <name>] [-decrypt-key <key>]\n")
		os.Exit(1)
	}

	key := *apiKey
	if key == "" {
		key = os.Getenv("GEMINI_API_KEY")
	}
	if key == "" {
		fmt.Fprintf(os.Stderr, "Error: Gemini API key required (-api-key or GEMINI_API_KEY env)\n")
		os.Exit(1)
	}

	dk := *decryptKey
	if dk == "" {
		dk = os.Getenv("DECRYPT_KEY")
	}

	ctx := context.Background()

	// 1. 解析聊天记录
	slog.Info("parsing chat history", "file", *inputFile, "format", *format)
	var conversations []parser.Conversation
	var messages []parser.ChatMessage

	detectedFormat := *format
	if detectedFormat == "auto" {
		ext := strings.ToLower(filepath.Ext(*inputFile))
		switch {
		case ext == ".enc":
			detectedFormat = "enc-jsonl"
		case ext == ".jsonl":
			detectedFormat = "jsonl"
		case ext == ".html" || ext == ".htm":
			detectedFormat = "html"
		default:
			detectedFormat = "text"
		}
	}

	switch detectedFormat {
	case "enc-jsonl":
		if dk == "" {
			fmt.Fprintf(os.Stderr, "Error: -decrypt-key required for .enc files\n")
			os.Exit(1)
		}
		plaintext, err := parser.DecryptFile(*inputFile, dk)
		if err != nil {
			slog.Error("decrypt failed", "error", err)
			os.Exit(1)
		}
		slog.Info("decrypted successfully", "bytes", len(plaintext))

		conversations, err = parser.ParseJSONLToConversations(plaintext, *myName, *targetName, *userIsMe)
		if err != nil {
			slog.Error("parse JSONL failed", "error", err)
			os.Exit(1)
		}

		// 同时提取扁平消息列表（用于风格分析）
		messages, err = parser.ParseJSONLBytes(plaintext, *myName, *targetName, *userIsMe)
		if err != nil {
			slog.Error("parse messages failed", "error", err)
			os.Exit(1)
		}

		// 清除内存中的明文
		for i := range plaintext {
			plaintext[i] = 0
		}

	case "jsonl":
		data, err := os.ReadFile(*inputFile)
		if err != nil {
			slog.Error("read file failed", "error", err)
			os.Exit(1)
		}
		conversations, err = parser.ParseJSONLToConversations(data, *myName, *targetName, *userIsMe)
		if err != nil {
			slog.Error("parse JSONL failed", "error", err)
			os.Exit(1)
		}
		messages, err = parser.ParseJSONLBytes(data, *myName, *targetName, *userIsMe)
		if err != nil {
			slog.Error("parse messages failed", "error", err)
			os.Exit(1)
		}

	case "html", "text":
		var err error
		if detectedFormat == "html" {
			messages, err = parser.ParseHTMLFile(*inputFile, *myName)
		} else {
			messages, err = parser.ParseTextFile(*inputFile, *myName)
		}
		if err != nil {
			slog.Error("parse failed", "error", err)
			os.Exit(1)
		}
		messages = parser.FilterTextOnly(messages)
		conversations = parser.SplitConversations(messages, 30)
	}

	slog.Info("parsed", "messages", len(messages), "conversations", len(conversations))

	// 2. 初始化 Gemini 客户端
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  key,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		slog.Error("create Gemini client failed", "error", err)
		os.Exit(1)
	}

	// 3. 风格分析
	slog.Info("analyzing speaking style...")
	p, err := analyzeStyle(ctx, client, messages, conversations, *myName, *targetName)
	if err != nil {
		slog.Error("style analysis failed", "error", err)
		os.Exit(1)
	}

	// 保存 persona.json
	personaPath := filepath.Join(*outputDir, "persona.json")
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		slog.Error("create output dir failed", "error", err)
		os.Exit(1)
	}
	personaData, _ := json.MarshalIndent(p, "", "  ")
	if err := os.WriteFile(personaPath, personaData, 0644); err != nil {
		slog.Error("write persona.json failed", "error", err)
		os.Exit(1)
	}
	slog.Info("saved persona", "path", personaPath)

	// 4. 构建 embedding 客户端池（多 key 轮换）
	var embedClients []*genai.Client
	embedClients = append(embedClients, client)

	key2 := *apiKey2
	if key2 == "" {
		key2 = os.Getenv("GEMINI_API_KEY2")
	}
	if key2 != "" {
		c2, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey:  key2,
			Backend: genai.BackendGeminiAPI,
		})
		if err == nil {
			embedClients = append(embedClients, c2)
		}
	}

	slog.Info("embedding clients ready", "count", len(embedClients))

	// 5. 向量化对话片段
	slog.Info("vectorizing conversations...")
	vectorsDir := filepath.Join(*outputDir, "vectors")
	if err := vectorize(ctx, embedClients, conversations, vectorsDir, *myName, *targetName); err != nil {
		slog.Error("vectorize failed", "error", err)
		os.Exit(1)
	}

	// 5. 生成导入报告（不输出任何聊天内容）
	report := fmt.Sprintf(`Import Report
=============
Conversations: %d
Messages:      %d
Vectors dir:   %s
Persona file:  %s
`, len(conversations), len(messages), vectorsDir, personaPath)

	reportPath := filepath.Join(*outputDir, "import_report.txt")
	os.WriteFile(reportPath, []byte(report), 0644)
	fmt.Println(report)
	slog.Info("done!")
}

func analyzeStyle(ctx context.Context, client *genai.Client, messages []parser.ChatMessage, conversations []parser.Conversation, myName, targetName string) (*persona.Persona, error) {
	var myMessages []string
	for _, m := range messages {
		if m.IsMe {
			myMessages = append(myMessages, m.Content)
		}
	}

	// 采样（最多500条）
	sample := myMessages
	if len(sample) > 500 {
		step := len(sample) / 500
		var sampled []string
		for i := 0; i < len(sample); i += step {
			sampled = append(sampled, sample[i])
		}
		sample = sampled
	}

	var convSamples []string
	for i, c := range conversations {
		if i >= 50 {
			break
		}
		convSamples = append(convSamples, c.FormatAsExample(myName, targetName))
	}

	prompt := fmt.Sprintf(`分析以下聊天记录中"%s"的说话风格。这是%s和%s之间的微信聊天记录。

## %s的消息样本（共%d条，采样%d条）：
%s

## 对话示例（%d段）：
%s

请输出严格的 JSON 格式（不要 markdown 代码块），包含以下字段：
{
  "style": {
    "typical_length": "描述消息长度特征",
    "catchphrases": ["口头禅1", "口头禅2"],
    "emoji_patterns": ["常用表情1", "常用表情2"],
    "punctuation_style": "标点使用特征",
    "response_style": "回复风格描述",
    "humor_style": "幽默风格描述",
    "formality": "正式程度",
    "multi_message": true/false,
    "negative_patterns": ["不会做的事1", "不会做的事2"],
    "greeting_examples": ["打招呼示例"],
    "agreement_examples": ["同意示例"],
    "refusal_examples": ["拒绝示例"]
  },
  "relationship": {
    "relationship": "关系描述",
    "shared_topics": ["共同话题1", "共同话题2"],
    "inside_jokes": ["内部梗/共同经历"],
    "tone": "对话语气特征",
    "key_facts": {"事实类别": "事实内容"}
  }
}`,
		myName, myName, targetName,
		myName, len(myMessages), len(sample),
		strings.Join(sample, "\n"),
		len(convSamples),
		strings.Join(convSamples, "\n---\n"),
	)

	resp, err := client.Models.GenerateContent(ctx, "gemini-2.5-flash",
		[]*genai.Content{genai.NewContentFromText(prompt, genai.RoleUser)},
		&genai.GenerateContentConfig{
			Temperature:     genai.Ptr(float32(0.3)),
			MaxOutputTokens: 8192,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("gemini analyze: %w", err)
	}

	text := resp.Text()
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	var p persona.Persona
	if err := json.Unmarshal([]byte(text), &p); err != nil {
		slog.Warn("failed to parse Gemini response as JSON, saving raw", "error", err)
		return &persona.Persona{}, nil
	}

	return &p, nil
}

func vectorize(ctx context.Context, clients []*genai.Client, conversations []parser.Conversation, vectorsDir string, myName, targetName string) error {
	if err := os.MkdirAll(vectorsDir, 0755); err != nil {
		return fmt.Errorf("create vectors dir: %w", err)
	}

	// 轮换计数器
	var callCount int64

	embedFunc := func(ctx context.Context, text string) ([]float32, error) {
		// 轮换使用不同的 API key
		idx := callCount % int64(len(clients))
		callCount++
		c := clients[idx]

		var lastErr error
		for attempt := 0; attempt < 5; attempt++ {
			resp, err := c.Models.EmbedContent(ctx, "gemini-embedding-001",
				[]*genai.Content{genai.NewContentFromText(text, genai.RoleUser)}, nil)
			if err == nil {
				if len(resp.Embeddings) == 0 {
					return nil, fmt.Errorf("empty embedding")
				}
				return resp.Embeddings[0].Values, nil
			}
			lastErr = err
			if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "RESOURCE_EXHAUSTED") {
				wait := time.Duration(10*(attempt+1)) * time.Second
				slog.Warn("rate limited, retrying", "attempt", attempt+1, "wait", wait)
				time.Sleep(wait)
				// 限流时切换到另一个 key
				idx = (idx + 1) % int64(len(clients))
				c = clients[idx]
				continue
			}
			return nil, err
		}
		return nil, fmt.Errorf("max retries: %w", lastErr)
	}

	db, err := chromem.NewPersistentDB(vectorsDir, false)
	if err != nil {
		return fmt.Errorf("create vector db: %w", err)
	}

	col, err := db.GetOrCreateCollection("conversations", nil, chromem.EmbeddingFunc(embedFunc))
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}

	// 断点续传：读取进度文件，跳过已完成的
	progressFile := filepath.Join(vectorsDir, ".progress")
	startFrom := 0
	if data, err := os.ReadFile(progressFile); err == nil {
		fmt.Sscanf(string(data), "%d", &startFrom)
		slog.Info("resuming from checkpoint", "start", startFrom)
	}

	var docs []chromem.Document
	for i, conv := range conversations {
		if i < startFrom {
			continue
		}

		text := conv.FormatAsExample(myName, targetName)
		if len(text) < 10 {
			continue
		}
		if len(text) > 2000 {
			text = text[:2000]
		}

		docs = append(docs, chromem.Document{
			ID:      fmt.Sprintf("conv_%05d", i),
			Content: text,
			Metadata: map[string]string{
				"msg_count": fmt.Sprintf("%d", len(conv.Messages)),
			},
		})

		if len(docs) >= 20 {
			slog.Info("vectorizing", "progress", fmt.Sprintf("%d/%d", i+1, len(conversations)))
			if err := col.AddDocuments(ctx, docs, 1); err != nil {
				return fmt.Errorf("add documents batch at %d: %w", i, err)
			}
			docs = docs[:0]
			// 保存进度
			os.WriteFile(progressFile, []byte(fmt.Sprintf("%d", i+1)), 0644)
			time.Sleep(500 * time.Millisecond)
		}
	}

	if len(docs) > 0 {
		slog.Info("vectorizing final batch", "count", len(docs))
		if err := col.AddDocuments(ctx, docs, 1); err != nil {
			return fmt.Errorf("add final documents: %w", err)
		}
	}

	// 完成后删除进度文件
	os.Remove(progressFile)

	slog.Info("vectorization complete", "total_vectors", col.Count())
	return nil
}
