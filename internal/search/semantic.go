package search

import (
	"fmt"
	"sort"

	"github.com/renderinc/slab-search/internal/embeddings"
	"github.com/renderinc/slab-search/internal/storage"
)

// SemanticSearch performs semantic similarity search using embeddings
// Returns results sorted by cosine similarity (highest first)
// useQwen: if true, uses EmbeddingQwen field; otherwise uses Embedding field
func (i *Index) SemanticSearch(queryEmbedding []float32, limit int, useQwen bool) ([]*SearchResult, error) {
	// 1. Get all documents from database (with embeddings)
	docs, err := i.db.List(false) // Don't include archived
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}

	// 2. Compute cosine similarity for each document
	type scoredDoc struct {
		doc   *storage.Document
		score float32
	}

	scores := make([]scoredDoc, 0, len(docs))
	for _, doc := range docs {
		// Select which embedding field to use
		var embeddingData []byte
		if useQwen {
			embeddingData = doc.EmbeddingQwen
		} else {
			embeddingData = doc.Embedding
		}

		// Skip documents without embeddings
		if len(embeddingData) == 0 {
			continue
		}

		docEmbedding := embeddings.DeserializeEmbedding(embeddingData)
		if docEmbedding == nil {
			continue
		}

		score := embeddings.CosineSimilarity(queryEmbedding, docEmbedding)
		scores = append(scores, scoredDoc{doc: doc, score: score})
	}

	// 3. Sort by score (descending)
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	// 4. Convert to SearchResult and return top N
	results := make([]*SearchResult, 0, limit)
	for i := 0; i < len(scores) && i < limit; i++ {
		doc := scores[i].doc
		results = append(results, &SearchResult{
			ID:      doc.ID,
			Title:   doc.Title,
			Author:  doc.AuthorName,
			SlabURL: doc.SlabURL,
			Score:   float64(scores[i].score),
		})
	}

	return results, nil
}

// HybridSearch combines keyword search (Bleve) with semantic search (embeddings)
// keywordWeight: 0.0-1.0, weight for keyword results (e.g., 0.7 = 70% keyword, 30% semantic)
// useQwen: if true, uses EmbeddingQwen field; otherwise uses Embedding field
func (i *Index) HybridSearch(query string, queryEmbedding []float32, limit int, keywordWeight float64, useQwen bool) ([]*SearchResult, error) {
	// Validate weight
	if keywordWeight < 0 || keywordWeight > 1 {
		return nil, fmt.Errorf("keywordWeight must be between 0 and 1")
	}
	semanticWeight := 1.0 - keywordWeight

	// 1. Perform both searches (get more candidates for better merging)
	candidateLimit := limit * 3 // Get 3x more candidates

	keywordResults, err := i.Search(query, candidateLimit)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}

	semanticResults, err := i.SemanticSearch(queryEmbedding, candidateLimit, useQwen)
	if err != nil {
		return nil, fmt.Errorf("semantic search: %w", err)
	}

	// 2. Normalize scores to 0-1 range for each result set
	keywordScores := normalizeScores(keywordResults)
	semanticScores := normalizeScores(semanticResults)

	// 3. Combine scores by document ID
	scoreMap := make(map[string]*SearchResult)

	// Add keyword results
	for _, result := range keywordResults {
		scoreMap[result.ID] = result
		result.Score = keywordScores[result.ID] * keywordWeight
	}

	// Merge semantic results
	for _, result := range semanticResults {
		if existing, found := scoreMap[result.ID]; found {
			// Document appears in both - combine scores
			existing.Score += semanticScores[result.ID] * semanticWeight
		} else {
			// Document only in semantic results
			result.Score = semanticScores[result.ID] * semanticWeight
			scoreMap[result.ID] = result
		}
	}

	// 4. Convert map to slice and sort by combined score
	combined := make([]*SearchResult, 0, len(scoreMap))
	for _, result := range scoreMap {
		combined = append(combined, result)
	}

	sort.Slice(combined, func(i, j int) bool {
		return combined[i].Score > combined[j].Score
	})

	// 5. Return top N
	if len(combined) > limit {
		combined = combined[:limit]
	}

	return combined, nil
}

// normalizeScores normalizes result scores to 0-1 range
// Returns a map of ID -> normalized score
func normalizeScores(results []*SearchResult) map[string]float64 {
	if len(results) == 0 {
		return make(map[string]float64)
	}

	// Find min and max scores
	minScore := results[0].Score
	maxScore := results[0].Score
	for _, r := range results {
		if r.Score < minScore {
			minScore = r.Score
		}
		if r.Score > maxScore {
			maxScore = r.Score
		}
	}

	// Normalize to 0-1 range
	normalized := make(map[string]float64, len(results))
	scoreRange := maxScore - minScore

	if scoreRange == 0 {
		// All scores are the same - assign 1.0 to all
		for _, r := range results {
			normalized[r.ID] = 1.0
		}
	} else {
		for _, r := range results {
			normalized[r.ID] = (r.Score - minScore) / scoreRange
		}
	}

	return normalized
}
