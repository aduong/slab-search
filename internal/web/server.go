package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"

	"github.com/renderinc/slab-search/internal/embeddings"
	"github.com/renderinc/slab-search/internal/search"
	"github.com/renderinc/slab-search/internal/storage"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Server struct {
	db        *storage.DB
	idx       *search.Index
	embedder  embeddings.Embedder
	templates *template.Template
}

type SearchRequest struct {
	Query         string  `json:"query"`
	Mode          string  `json:"mode"`           // "keyword", "semantic", "hybrid"
	HybridWeight  float64 `json:"hybrid_weight"`  // 0.0-1.0 (semantic weight)
	Limit         int     `json:"limit"`
}

type SearchResponse struct {
	Results []*search.SearchResult `json:"results"`
	Query   string                 `json:"query"`
	Mode    string                 `json:"mode"`
	Count   int                    `json:"count"`
	Error   string                 `json:"error,omitempty"`
}

func NewServer(db *storage.DB, idx *search.Index, embedder embeddings.Embedder) (*Server, error) {
	// Parse templates
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("error parsing templates: %w", err)
	}

	// Set DB reference for semantic search
	idx.SetDB(db)

	return &Server{
		db:        db,
		idx:       idx,
		embedder:  embedder,
		templates: tmpl,
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static files
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))

	// Routes
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/doc", s.handleGetDoc)
	mux.HandleFunc("/health", s.handleHealth)

	return mux
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	data := map[string]interface{}{
		"HasEmbeddings": s.embedder != nil,
	}

	if err := s.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("Error rendering template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		// Return empty state HTML
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<div class="empty-state">
			<p>👆 Start typing to search through your Slab documents</p>
			<div class="tips">
				<h3>Search Tips:</h3>
				<ul>
					<li><strong>"exact phrase"</strong> - Search for exact phrases</li>
					<li><strong>deploy~</strong> - Fuzzy matching (finds typos)</li>
					<li><strong>kubernetes docker</strong> - Multiple terms (AND search)</li>
				</ul>
			</div>
		</div>`)
		return
	}

	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "keyword"
	}

	limit := 20
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	hybridWeight := 0.3 // Default semantic weight for hybrid mode
	if weightStr := r.URL.Query().Get("weight"); weightStr != "" {
		if w, err := strconv.ParseFloat(weightStr, 64); err == nil && w >= 0 && w <= 1 {
			hybridWeight = w
		}
	}

	var results []*search.SearchResult
	var err error

	switch mode {
	case "semantic":
		if s.embedder == nil {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<div class="error">
				<strong>Error:</strong> Semantic search not available (Ollama not running)
			</div>`)
			return
		}

		queryEmbedding, err := s.embedder.Embed(query)
		if err != nil {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<div class="error">
				<strong>Error:</strong> Failed to generate embedding: %v
			</div>`, err)
			return
		}

		// For web UI, default to nomic embeddings (useQwen = false)
		results, err = s.idx.SemanticSearch(queryEmbedding, limit, false)

	case "hybrid":
		if s.embedder == nil {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<div class="error">
				<strong>Error:</strong> Hybrid search not available (Ollama not running)
			</div>`)
			return
		}

		queryEmbedding, err := s.embedder.Embed(query)
		if err != nil {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<div class="error">
				<strong>Error:</strong> Failed to generate embedding: %v
			</div>`, err)
			return
		}

		// hybridWeight is semantic weight, so keyword weight = 1 - hybridWeight
		// For web UI, default to nomic embeddings (useQwen = false)
		results, err = s.idx.HybridSearch(query, queryEmbedding, limit, 1-hybridWeight, false)

	default: // keyword
		results, err = s.idx.Search(query, limit)
	}

	if err != nil {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<div class="error">
			<strong>Error:</strong> Search failed: %v
		</div>`, err)
		return
	}

	// Render results as HTML
	w.Header().Set("Content-Type", "text/html")

	if len(results) == 0 {
		fmt.Fprintf(w, `<div class="no-results">
			<p>No results found for "<strong>%s</strong>"</p>
			<p class="hint">Try different keywords or use fuzzy search with ~ suffix</p>
		</div>`, template.HTMLEscapeString(query))
		return
	}

	// Results header
	fmt.Fprintf(w, `<div class="results-header">
		<p>Found <strong>%d</strong> results for "<strong>%s</strong>"</p>
		<p class="search-mode-indicator">Mode: <strong>%s</strong></p>
	</div>`, len(results), template.HTMLEscapeString(query), mode)

	// Render each result
	for i, result := range results {
		// Extract preview from fragments
		preview := ""
		if fragments, ok := result.Fragments["Content"]; ok && len(fragments) > 0 {
			preview = fragments[0]
		}

		fmt.Fprintf(w, `<div class="result-card">
			<div class="result-number">%d</div>
			<div class="result-content">
				<h3><a href="%s" target="_blank" rel="noopener">%s</a></h3>`,
			i+1,
			template.HTMLEscapeString(result.SlabURL),
			template.HTMLEscapeString(result.Title))

		if result.Author != "" {
			fmt.Fprintf(w, `<p class="result-meta">By %s</p>`, template.HTMLEscapeString(result.Author))
		}

		if preview != "" {
			fmt.Fprintf(w, `<p class="result-preview">%s</p>`, template.HTML(preview))
		}

		fmt.Fprintf(w, `<div class="result-footer">
				<span class="result-score">Score: %.3f</span>
				<a href="%s" target="_blank" rel="noopener" class="open-link">Open in Slab →</a>
			</div>
		</div>
	</div>`, result.Score, template.HTMLEscapeString(result.SlabURL))
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	dbCount, _ := s.db.Count()
	indexCount, _ := s.idx.Count()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "ok",
		"documents_in_db": dbCount,
		"documents_in_index": indexCount,
		"embeddings_available": s.embedder != nil,
	})
}

func (s *Server) handleGetDoc(w http.ResponseWriter, r *http.Request) {
	docID := r.URL.Query().Get("id")
	if docID == "" {
		http.Error(w, "Missing id parameter", http.StatusBadRequest)
		return
	}

	// Retrieve document from database
	doc, err := s.db.Get(docID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error retrieving document: %v", err), http.StatusInternalServerError)
		return
	}

	if doc == nil {
		http.Error(w, "Document not found", http.StatusNotFound)
		return
	}

	// Return markdown content
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Write([]byte(doc.Content))
}
