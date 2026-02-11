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

	"github.com/philippgille/chromem-go"
	"google.golang.org/genai"

	"github.com/liao/style-bot/internal/parser"
	"github.com/liao/style-bot/internal/persona"
)

func main() {
	inputFile := flag.String("input", "", "WechatExporter exported file (text or html)")
	outputDir := flag.String("output", "./data", "output directory")
	myName := flag.String("me", "我", "my display name in chat history")
	targetName := flag.String("target", "", "target person's display name")
	apiKey := flag.String("api-key", "", "Gemini API key (or set GEMINI_API_KEY env)")
	format := flag.String("format", "auto", "input format: text, html, auto")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))

	if *inputFile == "" || *targetName == "" {
		fmt.Fprintf(os.Stderr, "Usage: data-importer -input <file> -target <name> [-me <name>] [-api-key <key>]\n")
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

	ctx := context.Background()

	// 1. 解析聊天记录
	slog.Info("parsing chat history", "file", *inputFile, "format", *format)
	messages, err := parseFile(*inputFile, *myName, *format)
	if err != nil {
		slog.Error("parse failed", "error", err)
		os.Exit(1)
	}
	slog.Info("parsed messages", "total", len(messages))

	// 2. 过滤非文本消息
	messages = parser.FilterTextOnly(messages)
	slog.Info("after filter", "text_only", len(messages))

	// 3. 切分对话片段
	conversations := parser.SplitConversations(messages, 30)
	slog.Info("split conversations", "count", len(conversations))

	// 4. 初始化 Gemini 客户端
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  key,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		slog.Error("create Gemini client failed", "error", err)
		os.Exit(1)
	}

	// 5. 风格分析
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

	// 6. 向量化对话片段
	slog.Info("vectorizing conversations...")
	vectorsDir := filepath.Join(*outputDir, "vectors")
	if err := vectorize(ctx, client, conversations, vectorsDir, *myName, *targetName); err != nil {
		slog.Error("vectorize failed", "error", err)
		os.Exit(1)
	}

	// 7. 生成导入报告
	report := fmt.Sprintf(`Import Report
=============
Input file:     %s
Total messages: %d
Text messages:  %d
Conversations:  %d
Vectors dir:    %s
Persona file:   %s
`, *inputFile, len(messages), len(parser.FilterTextOnly(messages)), len(conversations), vectorsDir, personaPath)

	reportPath := filepath.Join(*outputDir, "import_report.txt")
	os.WriteFile(reportPath, []byte(report), 0644)
	fmt.Println(report)
	slog.Info("done!")
}

func parseFile(path, myName, format string) ([]parser.ChatMessage, error) {
	if format == "auto" {
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".html" || ext == ".htm" {
			format = "html"
		} else {
			format = "text"
		}
	}

	switch format {
	case "html":
		return parser.ParseHTMLFile(path, myName)
	case "text":
		return parser.ParseTextFile(path, myName)
	default:
		return nil, fmt.Errorf("unknown format: %s", format)
	}
}

func analyzeStyle(ctx context.Context, client *genai.Client, messages []parser.ChatMessage, conversations []parser.Conversation, myName, targetName string) (*persona.Persona, error) {
	// 收集我的消息样本
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

	// 收集对话样本
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

	resp, err := client.Models.GenerateContent(ctx, "gemini-3-pro-preview",
		[]*genai.Content{genai.NewContentFromText(prompt, genai.RoleUser)},
		&genai.GenerateContentConfig{
			Temperature:   genai.Ptr(float32(0.3)),
			MaxOutputTokens: 4096,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("gemini analyze: %w", err)
	}

	text := resp.Text()
	// 清理可能的 markdown 代码块标记
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	var p persona.Persona
	if err := json.Unmarshal([]byte(text), &p); err != nil {
		slog.Warn("failed to parse Gemini response as JSON, saving raw", "error", err)
		// 返回空 persona，让用户手动编辑
		return &persona.Persona{}, nil
	}

	return &p, nil
}

func vectorize(ctx context.Context, client *genai.Client, conversations []parser.Conversation, vectorsDir string, myName, targetName string) error {
	if err := os.MkdirAll(vectorsDir, 0755); err != nil {
		return fmt.Errorf("create vectors dir: %w", err)
	}

	embedFunc := func(ctx context.Context, text string) ([]float32, error) {
		resp, err := client.Models.EmbedContent(ctx, "text-embedding-004",
			[]*genai.Content{genai.NewContentFromText(text, genai.RoleUser)}, nil)
		if err != nil {
			return nil, err
		}
		if len(resp.Embeddings) == 0 {
			return nil, fmt.Errorf("empty embedding")
		}
		return resp.Embeddings[0].Values, nil
	}

	db, err := chromem.NewPersistentDB(vectorsDir, false)
	if err != nil {
		return fmt.Errorf("create vector db: %w", err)
	}

	col, err := db.GetOrCreateCollection("conversations", nil, chromem.EmbeddingFunc(embedFunc))
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}

	// 批量添加文档
	var docs []chromem.Document
	for i, conv := range conversations {
		text := conv.FormatAsExample(myName, targetName)
		if len(text) < 10 {
			continue
		}

		// 截断过长的对话
		if len(text) > 2000 {
			text = text[:2000]
		}

		docs = append(docs, chromem.Document{
			ID:      fmt.Sprintf("conv_%05d", i),
			Content: text,
			Metadata: map[string]string{
				"start_date": conv.StartAt.Format("2006-01-02"),
				"msg_count":  fmt.Sprintf("%d", len(conv.Messages)),
			},
		})

		// 每50个文档批量写入一次（避免 API 限流）
		if len(docs) >= 50 {
			slog.Info("writing batch", "count", len(docs), "total_done", i+1)
			if err := col.AddDocuments(ctx, docs, 4); err != nil {
				return fmt.Errorf("add documents batch: %w", err)
			}
			docs = docs[:0]
		}
	}

	// 写入剩余
	if len(docs) > 0 {
		slog.Info("writing final batch", "count", len(docs))
		if err := col.AddDocuments(ctx, docs, 4); err != nil {
			return fmt.Errorf("add final documents: %w", err)
		}
	}

	slog.Info("vectorization complete", "total_vectors", col.Count())
	return nil
}
