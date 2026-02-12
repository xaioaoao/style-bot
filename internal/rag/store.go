package rag

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"

	"github.com/philippgille/chromem-go"
)

type Store struct {
	db         *chromem.DB
	collection *chromem.Collection
}

// NewStore 创建或加载向量存储
func NewStore(vectorsDir string, embedFunc chromem.EmbeddingFunc) (*Store, error) {
	db, err := chromem.NewPersistentDB(vectorsDir, false)
	if err != nil {
		return nil, fmt.Errorf("open vector db: %w", err)
	}

	col, err := db.GetOrCreateCollection("conversations", nil, embedFunc)
	if err != nil {
		return nil, fmt.Errorf("get/create collection: %w", err)
	}

	slog.Info("vector store loaded", "dir", vectorsDir, "count", col.Count())
	return &Store{db: db, collection: col}, nil
}

// Query 检索相似对话
func (s *Store) Query(ctx context.Context, text string, topK int, minSimilarity float32) ([]Result, error) {
	if s.collection.Count() == 0 {
		return nil, nil
	}

	k := topK
	if k > s.collection.Count() {
		k = s.collection.Count()
	}

	docs, err := s.collection.Query(ctx, text, k, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("query vectors: %w", err)
	}

	var results []Result
	for _, d := range docs {
		if d.Similarity < minSimilarity {
			continue
		}
		results = append(results, Result{
			Content:    d.Content,
			Similarity: d.Similarity,
			Metadata:   d.Metadata,
		})
	}
	return results, nil
}

// AddDocuments 批量写入文档
func (s *Store) AddDocuments(ctx context.Context, docs []chromem.Document) error {
	return s.collection.AddDocuments(ctx, docs, runtime.NumCPU())
}

// Count 返回文档数量
func (s *Store) Count() int {
	return s.collection.Count()
}

type Result struct {
	Content    string
	Similarity float32
	Metadata   map[string]string
}
