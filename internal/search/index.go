package search

import (
	"fmt"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"

	"github.com/renderinc/slab-search/internal/storage"
)

// Index wraps a Bleve search index
type Index struct {
	index bleve.Index
}

// IndexedDocument represents a document in the search index
type IndexedDocument struct {
	ID          string
	Title       string
	Content     string
	Author      string
	Topics      []string
	PublishedAt time.Time
	UpdatedAt   time.Time
	SlabURL     string
}

// SearchResult represents a search result
type SearchResult struct {
	ID        string
	Title     string
	Author    string
	SlabURL   string
	Score     float64
	Fragments map[string][]string // Highlighted snippets
}

// Open opens or creates a Bleve index
func Open(path string) (*Index, error) {
	var idx bleve.Index
	var err error

	// Try to open existing index
	idx, err = bleve.Open(path)
	if err == bleve.ErrorIndexPathDoesNotExist {
		// Create new index with custom mapping
		indexMapping := buildIndexMapping()
		idx, err = bleve.New(path, indexMapping)
		if err != nil {
			return nil, fmt.Errorf("create index: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}

	return &Index{index: idx}, nil
}

// buildIndexMapping creates a custom index mapping with improved analyzers
func buildIndexMapping() mapping.IndexMapping {
	// Content field - use English analyzer for better stemming and stopword removal
	contentFieldMapping := bleve.NewTextFieldMapping()
	contentFieldMapping.Analyzer = "en"

	// Title field - use English analyzer (boost applied at query time)
	titleFieldMapping := bleve.NewTextFieldMapping()
	titleFieldMapping.Analyzer = "en"

	// Author field - keep default analyzer (good for names, no stemming)
	authorFieldMapping := bleve.NewTextFieldMapping()

	// Create document mapping
	docMapping := bleve.NewDocumentMapping()
	docMapping.AddFieldMappingsAt("ID", bleve.NewTextFieldMapping())
	docMapping.AddFieldMappingsAt("Title", titleFieldMapping)
	docMapping.AddFieldMappingsAt("Content", contentFieldMapping)
	docMapping.AddFieldMappingsAt("Author", authorFieldMapping)
	docMapping.AddFieldMappingsAt("SlabURL", bleve.NewTextFieldMapping())

	// Create index mapping
	indexMapping := bleve.NewIndexMapping()
	indexMapping.AddDocumentMapping("_default", docMapping)

	return indexMapping
}

// Close closes the index
func (i *Index) Close() error {
	return i.index.Close()
}

// Index adds or updates a document in the index
func (i *Index) IndexDocument(doc *IndexedDocument) error {
	return i.index.Index(doc.ID, doc)
}

// Delete removes a document from the index
func (i *Index) Delete(id string) error {
	return i.index.Delete(id)
}

// Search performs a search query with fuzzy matching
func (i *Index) Search(queryStr string, limit int) ([]*SearchResult, error) {
	// Parse query string (supports quotes, boolean operators, fuzzy ~)
	query := bleve.NewQueryStringQuery(queryStr)

	// Create search request with highlighting
	search := bleve.NewSearchRequestOptions(query, limit, 0, false)
	search.Highlight = bleve.NewHighlightWithStyle("html")
	search.Fields = []string{"Title", "Author", "SlabURL"}

	// Execute search
	results, err := i.index.Search(search)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	// Convert to our result type
	var searchResults []*SearchResult
	for _, hit := range results.Hits {
		result := &SearchResult{
			ID:        hit.ID,
			Score:     hit.Score,
			Fragments: hit.Fragments,
		}

		// Extract fields
		if title, ok := hit.Fields["Title"].(string); ok {
			result.Title = title
		}
		if author, ok := hit.Fields["Author"].(string); ok {
			result.Author = author
		}
		if url, ok := hit.Fields["SlabURL"].(string); ok {
			result.SlabURL = url
		}

		searchResults = append(searchResults, result)
	}

	return searchResults, nil
}

// IndexFromStorage indexes all documents from storage
func (i *Index) IndexFromStorage(db *storage.DB) error {
	docs, err := db.List(false) // Don't include archived
	if err != nil {
		return fmt.Errorf("list documents: %w", err)
	}

	batch := i.index.NewBatch()
	for _, doc := range docs {
		indexDoc := &IndexedDocument{
			ID:          doc.ID,
			Title:       doc.Title,
			Content:     doc.Content,
			Author:      doc.AuthorName,
			PublishedAt: doc.PublishedAt,
			UpdatedAt:   doc.UpdatedAt,
			SlabURL:     doc.SlabURL,
		}

		if err := batch.Index(indexDoc.ID, indexDoc); err != nil {
			return fmt.Errorf("batch index %s: %w", doc.ID, err)
		}
	}

	if err := i.index.Batch(batch); err != nil {
		return fmt.Errorf("commit batch: %w", err)
	}

	return nil
}

// Count returns the number of documents in the index
func (i *Index) Count() (uint64, error) {
	return i.index.DocCount()
}

// Rebuild completely rebuilds the index from storage with progress callback
// This is useful when changing index configuration or fixing corruption
func (i *Index) Rebuild(db *storage.DB, progressFn func(current, total int)) error {
	// Get all documents first
	docs, err := db.List(false) // Don't include archived
	if err != nil {
		return fmt.Errorf("list documents: %w", err)
	}

	totalDocs := len(docs)

	// Delete all documents from index
	docCount, err := i.index.DocCount()
	if err != nil {
		return fmt.Errorf("get doc count: %w", err)
	}

	if docCount > 0 {
		// Query all document IDs
		query := bleve.NewMatchAllQuery()
		search := bleve.NewSearchRequest(query)
		search.Size = int(docCount)
		search.Fields = []string{} // Only need IDs

		results, err := i.index.Search(search)
		if err != nil {
			return fmt.Errorf("query existing docs: %w", err)
		}

		// Delete all existing documents
		batch := i.index.NewBatch()
		for _, hit := range results.Hits {
			batch.Delete(hit.ID)
		}
		if err := i.index.Batch(batch); err != nil {
			return fmt.Errorf("delete existing docs: %w", err)
		}
	}

	// Index all documents from storage with progress reporting
	batchSize := 100
	for start := 0; start < totalDocs; start += batchSize {
		end := min(start + batchSize, totalDocs)

		batch := i.index.NewBatch()
		for _, doc := range docs[start:end] {
			indexDoc := &IndexedDocument{
				ID:          doc.ID,
				Title:       doc.Title,
				Content:     doc.Content,
				Author:      doc.AuthorName,
				PublishedAt: doc.PublishedAt,
				UpdatedAt:   doc.UpdatedAt,
				SlabURL:     doc.SlabURL,
			}

			if err := batch.Index(indexDoc.ID, indexDoc); err != nil {
				return fmt.Errorf("batch index %s: %w", doc.ID, err)
			}
		}

		if err := i.index.Batch(batch); err != nil {
			return fmt.Errorf("commit batch: %w", err)
		}

		// Report progress
		if progressFn != nil {
			progressFn(end, totalDocs)
		}
	}

	return nil
}
