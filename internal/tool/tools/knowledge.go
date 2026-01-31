package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"groq-go/internal/knowledge"
	"groq-go/internal/tool"
)

// KnowledgeSearchTool searches the knowledge base
type KnowledgeSearchTool struct {
	kb *knowledge.KnowledgeBase
}

func NewKnowledgeSearchTool(kb *knowledge.KnowledgeBase) *KnowledgeSearchTool {
	return &KnowledgeSearchTool{kb: kb}
}

func (t *KnowledgeSearchTool) Name() string {
	return "KnowledgeSearch"
}

func (t *KnowledgeSearchTool) Description() string {
	return "Search the knowledge base for relevant information. Use this to find context from uploaded documents before answering questions about specific topics."
}

func (t *KnowledgeSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The search query to find relevant information",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results to return (default: 5, max: 20)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *KnowledgeSearchTool) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	if t.kb == nil {
		return tool.Result{Content: "Knowledge base not available", IsError: true}, nil
	}

	var params struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}

	if err := json.Unmarshal(args, &params); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}

	if params.Query == "" {
		return tool.Result{Content: "Query is required", IsError: true}, nil
	}

	if params.MaxResults <= 0 {
		params.MaxResults = 5
	}
	if params.MaxResults > 20 {
		params.MaxResults = 20
	}

	results := t.kb.Search(ctx, params.Query, params.MaxResults)

	if len(results) == 0 {
		return tool.Result{Content: "No relevant information found in the knowledge base."}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d relevant results:\n\n", len(results)))

	for i, r := range results {
		sb.WriteString(fmt.Sprintf("--- Result %d (from: %s, score: %.2f) ---\n", i+1, r.DocName, r.Score))
		sb.WriteString(r.Chunk.Text)
		sb.WriteString("\n\n")
	}

	return tool.Result{Content: sb.String()}, nil
}

// KnowledgeListTool lists documents in the knowledge base
type KnowledgeListTool struct {
	kb *knowledge.KnowledgeBase
}

func NewKnowledgeListTool(kb *knowledge.KnowledgeBase) *KnowledgeListTool {
	return &KnowledgeListTool{kb: kb}
}

func (t *KnowledgeListTool) Name() string {
	return "KnowledgeList"
}

func (t *KnowledgeListTool) Description() string {
	return "List all documents in the knowledge base."
}

func (t *KnowledgeListTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []string{},
	}
}

func (t *KnowledgeListTool) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	if t.kb == nil {
		return tool.Result{Content: "Knowledge base not available", IsError: true}, nil
	}

	docs := t.kb.ListDocuments(ctx)

	if len(docs) == 0 {
		return tool.Result{Content: "No documents in the knowledge base."}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Knowledge base contains %d documents:\n\n", len(docs)))

	for _, doc := range docs {
		sb.WriteString(fmt.Sprintf("- %s (ID: %s, added: %s)\n", doc.Name, doc.ID, doc.CreatedAt.Format("2006-01-02 15:04")))
	}

	return tool.Result{Content: sb.String()}, nil
}
