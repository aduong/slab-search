package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/renderinc/slab-search/internal/embeddings"
	"github.com/renderinc/slab-search/internal/search"
	"github.com/renderinc/slab-search/internal/slab"
	"github.com/renderinc/slab-search/internal/storage"
	"github.com/renderinc/slab-search/internal/sync"
)

const (
	dataDir    = "./data"
	dbPath     = "./data/slab.db"
	indexPath  = "./data/bleve"
	ollamaURL  = "http://localhost:11434"
	ollamaModel = "nomic-embed-text"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "sync":
		runSync()
	case "search":
		// Parse search flags
		searchFlags := flag.NewFlagSet("search", flag.ExitOnError)
		semantic := searchFlags.Bool("semantic", false, "Use semantic search only")
		hybrid := searchFlags.Float64("hybrid", 0.0, "Use hybrid search (0.0-1.0, where value is semantic weight)")

		searchFlags.Parse(os.Args[2:])

		if searchFlags.NArg() < 1 {
			fmt.Println("Error: search query required")
			fmt.Println("Usage: slab-search search [flags] <query>")
			os.Exit(1)
		}

		query := strings.Join(searchFlags.Args(), " ")
		runSearch(query, *semantic, *hybrid)
	case "reindex":
		runReindex()
	case "stats":
		runStats()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Slab Search - Fast search for Slab documents")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  slab-search sync                     Sync posts from Slab + generate embeddings (if Ollama running)")
	fmt.Println("  slab-search search [flags] <query>   Search for documents")
	fmt.Println("  slab-search reindex                  Rebuild index + regenerate embeddings (if Ollama running)")
	fmt.Println("  slab-search stats                    Show index statistics")
	fmt.Println()
	fmt.Println("Search Flags:")
	fmt.Println("  -semantic         Use semantic search only (requires embeddings)")
	fmt.Println("  -hybrid=<weight>  Use hybrid search (0.0-1.0 semantic weight, default keyword-only)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  slab-search sync")
	fmt.Println("  slab-search search kubernetes                    # Keyword search")
	fmt.Println("  slab-search search \"postgres config\"              # Phrase search")
	fmt.Println("  slab-search search 'deploy~'                     # Fuzzy search")
	fmt.Println("  slab-search search -semantic \"database scaling\"  # Semantic search only")
	fmt.Println("  slab-search search -hybrid=0.3 kubernetes        # Hybrid (70% keyword, 30% semantic)")
	fmt.Println("  slab-search reindex                              # Rebuild index without re-syncing")
}

func runSync() {
	// Read token from file or env
	token := getToken()
	if token == "" {
		log.Fatal("Error: SLAB_TOKEN environment variable or ./token file required")
	}

	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("Error creating data directory: %v", err)
	}

	// Initialize components
	slabClient := slab.NewClient(token)

	db, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}
	defer db.Close()

	idx, err := search.Open(indexPath)
	if err != nil {
		log.Fatalf("Error opening search index: %v", err)
	}
	defer idx.Close()

	// Try to initialize embeddings client (optional - graceful degradation)
	var embedder *embeddings.Client
	embedder = embeddings.NewClient(ollamaURL, ollamaModel)
	if err := embedder.Health(); err != nil {
		log.Printf("Warning: Ollama not available (%v), skipping embedding generation", err)
		log.Printf("To enable semantic search, install Ollama and run: ollama pull %s", ollamaModel)
		embedder = nil // Disable embeddings
	} else {
		log.Printf("✓ Ollama available, will generate embeddings with %s", ollamaModel)
	}

	// Create sync worker (0 = unlimited)
	worker := sync.NewWorker(slabClient, db, idx, embedder, 0)

	// Run sync
	ctx := context.Background()
	stats, err := worker.Sync(ctx)
	if err != nil {
		log.Fatalf("Error syncing: %v", err)
	}

	// Print summary
	fmt.Println()
	fmt.Println("=== Sync Complete ===")
	fmt.Printf("Total posts:   %d\n", stats.TotalPosts)
	fmt.Printf("New:           %d\n", stats.NewPosts)
	fmt.Printf("Updated:       %d\n", stats.UpdatedPosts)
	fmt.Printf("Skipped:       %d\n", stats.SkippedPosts)
	if embedder != nil {
		fmt.Printf("Embeddings:    %d generated, %d failed\n", stats.EmbeddingsGen, stats.EmbeddingsFailed)
	}
	fmt.Printf("Errors:        %d\n", stats.Errors)
	fmt.Printf("Duration:      %v\n", stats.Duration)
}

func runSearch(query string, semanticOnly bool, hybridWeight float64) {
	// Open database
	db, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}
	defer db.Close()

	// Open search index
	idx, err := search.Open(indexPath)
	if err != nil {
		log.Fatalf("Error opening search index: %v", err)
	}
	defer idx.Close()

	// Set DB reference for semantic search
	idx.SetDB(db)

	var results []*search.SearchResult

	// Determine search mode
	if semanticOnly || hybridWeight > 0 {
		// Initialize embeddings client for semantic/hybrid search
		embedder := embeddings.NewClient(ollamaURL, ollamaModel)
		if err := embedder.Health(); err != nil {
			log.Fatalf("Error: Semantic search requires Ollama. Please install and run: ollama pull %s", ollamaModel)
		}

		// Generate query embedding
		queryEmbedding, err := embedder.Embed(query)
		if err != nil {
			log.Fatalf("Error generating query embedding: %v", err)
		}

		if semanticOnly {
			// Pure semantic search
			fmt.Println("Using semantic search...")
			results, err = idx.SemanticSearch(queryEmbedding, 10)
		} else {
			// Hybrid search
			fmt.Printf("Using hybrid search (%.0f%% keyword, %.0f%% semantic)...\n",
				(1-hybridWeight)*100, hybridWeight*100)
			results, err = idx.HybridSearch(query, queryEmbedding, 10, 1-hybridWeight)
		}

		if err != nil {
			log.Fatalf("Error searching: %v", err)
		}
	} else {
		// Pure keyword search (default)
		fmt.Println("Using keyword search...")
		results, err = idx.Search(query, 10)
		if err != nil {
			log.Fatalf("Error searching: %v", err)
		}
	}

	// Display results
	if len(results) == 0 {
		fmt.Println("No results found")
		return
	}

	fmt.Printf("\nFound %d results:\n\n", len(results))

	for i, result := range results {
		fmt.Printf("%d. %s\n", i+1, result.Title)
		if result.Author != "" {
			fmt.Printf("   Author: %s\n", result.Author)
		}
		fmt.Printf("   URL: %s\n", result.SlabURL)
		fmt.Printf("   Score: %.3f\n", result.Score)

		// Show content snippets if available (keyword search only)
		if snippets, ok := result.Fragments["Content"]; ok && len(snippets) > 0 {
			fmt.Printf("   Preview: %s\n", snippets[0])
		}
		fmt.Println()
	}
}

func runStats() {
	// Open database
	db, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}
	defer db.Close()

	// Open search index
	idx, err := search.Open(indexPath)
	if err != nil {
		log.Fatalf("Error opening search index: %v", err)
	}
	defer idx.Close()

	// Get stats
	dbCount, err := db.Count()
	if err != nil {
		log.Fatalf("Error getting database count: %v", err)
	}

	indexCount, err := idx.Count()
	if err != nil {
		log.Fatalf("Error getting index count: %v", err)
	}

	fmt.Println("=== Index Statistics ===")
	fmt.Printf("Documents in database: %d\n", dbCount)
	fmt.Printf("Documents in index:    %d\n", indexCount)
}

func runReindex() {
	fmt.Println("Rebuilding search index and embeddings from database...")
	fmt.Println()

	// Open database
	db, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}
	defer db.Close()

	// Open search index
	idx, err := search.Open(indexPath)
	if err != nil {
		log.Fatalf("Error opening search index: %v", err)
	}
	defer idx.Close()

	// Try to initialize embeddings client (optional)
	var embedder *embeddings.Client
	embedder = embeddings.NewClient(ollamaURL, ollamaModel)
	if err := embedder.Health(); err != nil {
		log.Printf("Warning: Ollama not available (%v), skipping embedding generation", err)
		log.Printf("To generate embeddings, install Ollama and run: ollama pull %s", ollamaModel)
		embedder = nil
	} else {
		log.Printf("✓ Ollama available, will generate embeddings with %s", ollamaModel)
	}

	// Get documents
	docs, err := db.List(false)
	if err != nil {
		log.Fatalf("Error listing documents: %v", err)
	}

	fmt.Printf("Found %d documents in database\n", len(docs))
	startTime := time.Now()

	// Generate embeddings if Ollama is available
	if embedder != nil {
		fmt.Println("Generating embeddings...")
		embeddingsGenerated := 0
		embeddingsFailed := 0

		for i, doc := range docs {
			// Show progress every 100 documents
			if i > 0 && i%100 == 0 {
				percent := float64(i) / float64(len(docs)) * 100
				fmt.Printf("\rEmbeddings: %d/%d (%.1f%%) - %d generated, %d failed  ",
					i, len(docs), percent, embeddingsGenerated, embeddingsFailed)
			}

			// Generate embedding
			textToEmbed := fmt.Sprintf("%s\n\n%s", doc.Title, doc.Content)
			embedding, err := embedder.Embed(textToEmbed)
			if err != nil {
				log.Printf("\nWarning: Failed to generate embedding for %s: %v", doc.ID, err)
				embeddingsFailed++
				continue
			}

			// Update document with embedding
			doc.Embedding = embeddings.SerializeEmbedding(embedding)
			if err := db.Upsert(doc); err != nil {
				log.Printf("\nWarning: Failed to update embedding for %s: %v", doc.ID, err)
				embeddingsFailed++
				continue
			}

			embeddingsGenerated++
		}

		fmt.Printf("\rEmbeddings: %d/%d (100.0%%) - %d generated, %d failed\n",
			len(docs), len(docs), embeddingsGenerated, embeddingsFailed)
		fmt.Println()
	}

	// Rebuild Bleve index
	fmt.Println("Rebuilding Bleve keyword index...")
	progressFn := func(current, total int) {
		percent := float64(current) / float64(total) * 100
		fmt.Printf("\rIndexing: %d/%d (%.1f%%)  ", current, total, percent)
	}

	if err := idx.Rebuild(db, progressFn); err != nil {
		log.Fatalf("\nError rebuilding index: %v", err)
	}

	duration := time.Since(startTime)

	// Get final index count
	indexCount, err := idx.Count()
	if err != nil {
		log.Fatalf("\nError getting index count: %v", err)
	}

	fmt.Println() // New line after progress
	fmt.Println()
	fmt.Println("=== Reindex Complete ===")
	fmt.Printf("Documents indexed: %d\n", indexCount)
	fmt.Printf("Duration:          %v\n", duration)
}

func getToken() string {
	// Try environment variable first
	if token := os.Getenv("SLAB_TOKEN"); token != "" {
		return token
	}

	// Try reading from token file
	tokenBytes, err := os.ReadFile("./token")
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(tokenBytes))
}
