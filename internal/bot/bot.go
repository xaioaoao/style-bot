package bot

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/driver"
	"github.com/wdvxdr1123/ZeroBot/message"

	"github.com/liao/style-bot/internal/ai"
	"github.com/liao/style-bot/internal/chat"
	"github.com/liao/style-bot/internal/config"
	"github.com/liao/style-bot/internal/persona"
	"github.com/liao/style-bot/internal/rag"
)

type Bot struct {
	cfg      *config.Config
	ai       *ai.Client
	chat     *chat.Manager
	rag      *rag.Pipeline
	persona  *persona.Persona
	cancel   context.CancelFunc
}

func New(cfg *config.Config, aiClient *ai.Client, chatMgr *chat.Manager, ragPipeline *rag.Pipeline, p *persona.Persona) *Bot {
	return &Bot{
		cfg:     cfg,
		ai:      aiClient,
		chat:    chatMgr,
		rag:     ragPipeline,
		persona: p,
	}
}

func (b *Bot) Run(ctx context.Context) {
	ctx, b.cancel = context.WithCancel(ctx)

	ws := driver.NewWebSocketClient(
		b.cfg.NapCat.WSURL,
		b.cfg.NapCat.AccessToken,
	)

	// 注册私聊消息处理
	zero.OnMessage(zero.OnlyPrivate, b.targetFilter()).Handle(func(zctx *zero.Ctx) {
		b.handleMessage(ctx, zctx)
	})

	// 管理命令：owner 发 /status 查看状态
	zero.OnCommand("status", zero.OnlyPrivate, b.ownerFilter()).Handle(func(zctx *zero.Ctx) {
		zctx.Send(message.Text("style-bot running"))
	})

	slog.Info("bot starting",
		"target_qq", b.cfg.Bot.TargetQQ,
		"ws_url", b.cfg.NapCat.WSURL,
	)

	zero.RunAndBlock(&zero.Config{
		NickName:   []string{"style-bot"},
		SuperUsers: []int64{b.cfg.Bot.OwnerQQ},
		Driver:     []zero.Driver{ws},
	}, nil)
}

func (b *Bot) Stop() {
	if b.cancel != nil {
		b.cancel()
	}
	if err := b.chat.Save(); err != nil {
		slog.Error("save session failed", "error", err)
	}
}

func (b *Bot) handleMessage(ctx context.Context, zctx *zero.Ctx) {
	userMsg := strings.TrimSpace(zctx.ExtractPlainText())
	if userMsg == "" {
		return // 跳过纯表情/图片等非文本消息
	}

	slog.Info("received message", "from", zctx.Event.UserID, "text", userMsg)

	// 添加到会话上下文
	b.chat.AddUserMessage(userMsg)

	// RAG 检索相关示例
	examples, err := b.rag.Retrieve(ctx, userMsg)
	if err != nil {
		slog.Error("RAG retrieve failed", "error", err)
	}

	// 组装 system prompt
	styleText := ""
	relationText := ""
	if b.persona != nil {
		styleText = b.persona.FormatStyleForPrompt()
		relationText = b.persona.FormatRelationshipForPrompt(b.cfg.Bot.TargetName)
	}

	systemPrompt := ai.BuildSystemPrompt(
		b.cfg.Bot.MyName,
		b.cfg.Bot.TargetName,
		styleText,
		relationText,
		examples,
	)

	// 获取对话历史
	history := b.chat.GetHistory()
	// 最后一条是刚添加的 user message，从历史中排除（会作为 userMsg 传入）
	if len(history) > 0 {
		history = history[:len(history)-1]
	}

	// 调 Gemini 生成回复，失败时兜底
	reply, err := b.ai.GenerateChat(ctx, systemPrompt, history, userMsg)
	if err != nil {
		slog.Error("generate reply failed, using fallback", "error", err)
		// 兜底：清掉历史重试一次（可能是历史数据有问题）
		reply, err = b.ai.GenerateChat(ctx, systemPrompt, nil, userMsg)
		if err != nil {
			slog.Error("fallback also failed, sending simple reply", "error", err)
			// 最终兜底：从风格档案里随机挑一个回复
			reply = b.fallbackReply()
		}
	}

	// 后处理
	reply = ai.FilterAIPatterns(reply)

	// 分割多条消息并发送
	parts := ai.SplitMultiMessage(reply)
	for i, part := range parts {
		if i > 0 {
			delay := b.randomDelay()
			time.Sleep(delay)
		}
		part = ConvertWxEmoji(part)
		zctx.Send(message.Text(part))
	}

	// 记录 bot 回复到上下文
	b.chat.AddBotReply(reply)

	// 异步保存会话
	go func() {
		if err := b.chat.Save(); err != nil {
			slog.Error("save session failed", "error", err)
		}
	}()
}

func (b *Bot) targetFilter() zero.Rule {
	return func(ctx *zero.Ctx) bool {
		if b.cfg.Bot.TargetQQ == 0 {
			return true // 不限制，回复所有人
		}
		return ctx.Event.UserID == b.cfg.Bot.TargetQQ
	}
}

func (b *Bot) ownerFilter() zero.Rule {
	return func(ctx *zero.Ctx) bool {
		return ctx.Event.UserID == b.cfg.Bot.OwnerQQ
	}
}

func (b *Bot) fallbackReply() string {
	fallbacks := []string{"嗯嗯", "好呢", "哈哈", "嘻嘻", "在呢", "怎么啦", "好好好"}
	if b.persona != nil && len(b.persona.Style.AgreementExamples) > 0 {
		fallbacks = b.persona.Style.AgreementExamples
	}
	return fallbacks[rand.IntN(len(fallbacks))]
}

func (b *Bot) randomDelay() time.Duration {
	minMs := b.cfg.Bot.ReplyDelayMinMs
	maxMs := b.cfg.Bot.ReplyDelayMaxMs
	if maxMs <= minMs {
		return time.Duration(minMs) * time.Millisecond
	}
	ms := minMs + rand.IntN(maxMs-minMs)
	return time.Duration(ms) * time.Millisecond
}
