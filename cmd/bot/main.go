package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/liao/style-bot/internal/ai"
	"github.com/liao/style-bot/internal/bot"
	"github.com/liao/style-bot/internal/chat"
	"github.com/liao/style-bot/internal/config"
	"github.com/liao/style-bot/internal/persona"
	"github.com/liao/style-bot/internal/rag"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "config file path")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Gemini 客户端
	aiClient, err := ai.NewClient(ctx,
		cfg.Gemini.APIKey,
		cfg.Gemini.ChatModel,
		cfg.Gemini.EmbeddingModel,
		cfg.Gemini.Temperature,
		cfg.Gemini.MaxOutputTokens,
		cfg.Gemini.RPMLimit,
	)
	if err != nil {
		slog.Error("create AI client failed", "error", err)
		os.Exit(1)
	}
	slog.Info("AI client initialized", "model", cfg.Gemini.ChatModel)

	// 会话管理
	chatMgr, err := chat.NewManager(cfg.Bot.MaxContextTurns, cfg.Data.SessionsDir)
	if err != nil {
		slog.Error("create chat manager failed", "error", err)
		os.Exit(1)
	}

	// 向量存储 + RAG
	store, err := rag.NewStore(cfg.RAG.VectorsDir, aiClient.EmbedFunc())
	if err != nil {
		slog.Warn("load vector store failed, RAG disabled", "error", err)
		store = nil
	}
	ragPipeline := rag.NewPipeline(store, cfg.RAG.TopK, cfg.RAG.MinSimilarity)

	// Persona
	var p *persona.Persona
	if cfg.Data.PersonaFile != "" {
		p, err = persona.LoadFromFile(cfg.Data.PersonaFile)
		if err != nil {
			slog.Warn("load persona failed, using default", "error", err)
		}
	}

	// Bot
	b := bot.New(cfg, aiClient, chatMgr, ragPipeline, p)

	// 优雅关闭
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		slog.Info("shutting down...")
		b.Stop()
		cancel()
		os.Exit(0)
	}()

	b.Run(ctx)
}
