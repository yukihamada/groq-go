package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Document represents a document in the knowledge base
type Document struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Content   string    `json:"content"`
	Chunks    []Chunk   `json:"chunks"`
	CreatedAt time.Time `json:"created_at"`
}

// Chunk represents a text chunk from a document
type Chunk struct {
	ID       string `json:"id"`
	DocID    string `json:"doc_id"`
	Text     string `json:"text"`
	Position int    `json:"position"`
}

// SearchResult represents a search result
type SearchResult struct {
	Chunk   Chunk   `json:"chunk"`
	DocName string  `json:"doc_name"`
	Score   float64 `json:"score"`
}

// KnowledgeBase manages documents and search
type KnowledgeBase struct {
	dir       string
	documents map[string]*Document
	mu        sync.RWMutex
}

// NewKnowledgeBase creates a new knowledge base
func NewKnowledgeBase(dir string) (*KnowledgeBase, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	kb := &KnowledgeBase{
		dir:       dir,
		documents: make(map[string]*Document),
	}

	// Load existing documents
	if err := kb.loadDocuments(); err != nil {
		return nil, err
	}

	return kb, nil
}

// DefaultKnowledgeDir returns the default knowledge base directory
func DefaultKnowledgeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "groq-go", "knowledge")
}

// AddDocument adds a document to the knowledge base
func (kb *KnowledgeBase) AddDocument(ctx context.Context, name, content string) (*Document, error) {
	kb.mu.Lock()
	defer kb.mu.Unlock()

	doc := &Document{
		ID:        generateID(),
		Name:      name,
		Content:   content,
		CreatedAt: time.Now(),
	}

	// Split content into chunks
	doc.Chunks = kb.chunkText(doc.ID, content)

	kb.documents[doc.ID] = doc

	// Save to disk
	if err := kb.saveDocument(doc); err != nil {
		return nil, err
	}

	return doc, nil
}

// GetDocument retrieves a document by ID
func (kb *KnowledgeBase) GetDocument(ctx context.Context, id string) (*Document, error) {
	kb.mu.RLock()
	defer kb.mu.RUnlock()

	doc, ok := kb.documents[id]
	if !ok {
		return nil, fmt.Errorf("document not found: %s", id)
	}

	return doc, nil
}

// ListDocuments returns all document metadata
func (kb *KnowledgeBase) ListDocuments(ctx context.Context) []Document {
	kb.mu.RLock()
	defer kb.mu.RUnlock()

	docs := make([]Document, 0, len(kb.documents))
	for _, doc := range kb.documents {
		docs = append(docs, Document{
			ID:        doc.ID,
			Name:      doc.Name,
			CreatedAt: doc.CreatedAt,
		})
	}

	sort.Slice(docs, func(i, j int) bool {
		return docs[i].CreatedAt.After(docs[j].CreatedAt)
	})

	return docs
}

// DeleteDocument removes a document
func (kb *KnowledgeBase) DeleteDocument(ctx context.Context, id string) error {
	kb.mu.Lock()
	defer kb.mu.Unlock()

	if _, ok := kb.documents[id]; !ok {
		return fmt.Errorf("document not found: %s", id)
	}

	delete(kb.documents, id)

	// Remove from disk
	return os.Remove(filepath.Join(kb.dir, id+".json"))
}

// Search performs semantic search using BM25-like scoring
func (kb *KnowledgeBase) Search(ctx context.Context, query string, maxResults int) []SearchResult {
	kb.mu.RLock()
	defer kb.mu.RUnlock()

	if maxResults <= 0 {
		maxResults = 5
	}

	queryTerms := tokenize(query)
	if len(queryTerms) == 0 {
		return nil
	}

	// Calculate IDF for query terms
	idf := make(map[string]float64)
	totalDocs := 0
	for _, doc := range kb.documents {
		totalDocs += len(doc.Chunks)
	}

	for _, term := range queryTerms {
		docCount := 0
		for _, doc := range kb.documents {
			for _, chunk := range doc.Chunks {
				if strings.Contains(strings.ToLower(chunk.Text), term) {
					docCount++
					break
				}
			}
		}
		if docCount > 0 {
			idf[term] = math.Log(float64(totalDocs+1) / float64(docCount+1))
		}
	}

	// Score each chunk
	var results []SearchResult
	for _, doc := range kb.documents {
		for _, chunk := range doc.Chunks {
			score := kb.scoreChunk(chunk.Text, queryTerms, idf)
			if score > 0 {
				results = append(results, SearchResult{
					Chunk:   chunk,
					DocName: doc.Name,
					Score:   score,
				})
			}
		}
	}

	// Sort by score
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Limit results
	if len(results) > maxResults {
		results = results[:maxResults]
	}

	return results
}

func (kb *KnowledgeBase) scoreChunk(text string, queryTerms []string, idf map[string]float64) float64 {
	textLower := strings.ToLower(text)
	textTerms := tokenize(text)
	termFreq := make(map[string]int)
	for _, t := range textTerms {
		termFreq[t]++
	}

	// BM25 parameters
	k1 := 1.2
	b := 0.75
	avgDl := 100.0 // Average document length assumption
	dl := float64(len(textTerms))

	score := 0.0
	for _, term := range queryTerms {
		tf := float64(termFreq[term])
		if tf > 0 || strings.Contains(textLower, term) {
			if tf == 0 {
				tf = 1
			}
			idfScore := idf[term]
			tfScore := (tf * (k1 + 1)) / (tf + k1*(1-b+b*dl/avgDl))
			score += idfScore * tfScore
		}
	}

	return score
}

func (kb *KnowledgeBase) chunkText(docID, text string) []Chunk {
	// Split by paragraphs, then by sentences if paragraph is too long
	paragraphs := strings.Split(text, "\n\n")
	var chunks []Chunk
	position := 0

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		// If paragraph is short enough, use as one chunk
		if len(para) <= 500 {
			chunks = append(chunks, Chunk{
				ID:       fmt.Sprintf("%s-%d", docID, position),
				DocID:    docID,
				Text:     para,
				Position: position,
			})
			position++
			continue
		}

		// Split long paragraphs by sentences
		sentences := splitSentences(para)
		currentChunk := ""
		for _, sentence := range sentences {
			if len(currentChunk)+len(sentence) > 500 && currentChunk != "" {
				chunks = append(chunks, Chunk{
					ID:       fmt.Sprintf("%s-%d", docID, position),
					DocID:    docID,
					Text:     strings.TrimSpace(currentChunk),
					Position: position,
				})
				position++
				currentChunk = sentence
			} else {
				if currentChunk != "" {
					currentChunk += " "
				}
				currentChunk += sentence
			}
		}
		if currentChunk != "" {
			chunks = append(chunks, Chunk{
				ID:       fmt.Sprintf("%s-%d", docID, position),
				DocID:    docID,
				Text:     strings.TrimSpace(currentChunk),
				Position: position,
			})
			position++
		}
	}

	return chunks
}

func (kb *KnowledgeBase) saveDocument(doc *Document) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(kb.dir, doc.ID+".json"), data, 0644)
}

func (kb *KnowledgeBase) loadDocuments() error {
	entries, err := os.ReadDir(kb.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(kb.dir, entry.Name()))
		if err != nil {
			continue
		}

		var doc Document
		if err := json.Unmarshal(data, &doc); err != nil {
			continue
		}

		kb.documents[doc.ID] = &doc
	}

	return nil
}

func generateID() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 12)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
		time.Sleep(time.Nanosecond)
	}
	return string(b)
}

var wordRegex = regexp.MustCompile(`[a-zA-Z0-9]+`)

func tokenize(text string) []string {
	text = strings.ToLower(text)
	matches := wordRegex.FindAllString(text, -1)

	// Remove stopwords
	stopwords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"but": true, "in": true, "on": true, "at": true, "to": true,
		"for": true, "of": true, "with": true, "by": true, "from": true,
		"is": true, "are": true, "was": true, "were": true, "be": true,
		"been": true, "being": true, "have": true, "has": true, "had": true,
		"do": true, "does": true, "did": true, "will": true, "would": true,
		"could": true, "should": true, "may": true, "might": true,
		"this": true, "that": true, "these": true, "those": true,
		"it": true, "its": true, "i": true, "me": true, "my": true,
	}

	var tokens []string
	for _, match := range matches {
		if !stopwords[match] && len(match) > 1 {
			tokens = append(tokens, match)
		}
	}
	return tokens
}

var sentenceRegex = regexp.MustCompile(`[.!?]+\s+`)

func splitSentences(text string) []string {
	indices := sentenceRegex.FindAllStringIndex(text, -1)
	if len(indices) == 0 {
		return []string{text}
	}

	var sentences []string
	start := 0
	for _, idx := range indices {
		sentences = append(sentences, text[start:idx[1]])
		start = idx[1]
	}
	if start < len(text) {
		sentences = append(sentences, text[start:])
	}
	return sentences
}
