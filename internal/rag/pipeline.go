package rag

import (
	"context"
	"log/slog"
)

type Pipeline struct {
	store         *Store
	topK          int
	minSimilarity float32
}

func NewPipeline(store *Store, topK int, minSimilarity float32) *Pipeline {
	return &Pipeline{
		store:         store,
		topK:          topK,
		minSimilarity: minSimilarity,
	}
}

// Retrieve 根据用户消息检索相关的历史对话示例
func (p *Pipeline) Retrieve(ctx context.Context, userMsg string) ([]string, error) {
	if p.store == nil || p.store.Count() == 0 {
		slog.Debug("no vectors in store, skipping RAG")
		return nil, nil
	}

	results, err := p.store.Query(ctx, userMsg, p.topK, p.minSimilarity)
	if err != nil {
		return nil, err
	}

	examples := make([]string, 0, len(results))
	for _, r := range results {
		examples = append(examples, r.Content)
	}

	slog.Debug("RAG retrieved examples", "query", userMsg, "count", len(examples))
	return examples, nil
}
