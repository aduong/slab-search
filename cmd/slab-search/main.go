package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/renderinc/slab-search/internal/embeddings"
	"github.com/renderinc/slab-search/internal/search"
	"github.com/renderinc/slab-search/internal/slab"
	"github.com/renderinc/slab-search/internal/storage"
	"github.com/renderinc/slab-search/internal/sync"
	"github.com/renderinc/slab-search/internal/web"
)

const (
	ollamaURL   = "http://localhost:11434"
	ollamaModel = "nomic-embed-text"
)

var (
	dataDir   string
	dbPath    string
	indexPath string
)

func main() {
	// Parse global flags
	globalFlags := flag.NewFlagSet("global", flag.ExitOnError)
	dataDirFlag := globalFlags.String("data-dir", "./data", "Directory for database and index files")

	// Check if we have any arguments
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Find where the command starts (skip global flags)
	commandIdx := 1
	for i := 1; i < len(os.Args); i++ {
		if !strings.HasPrefix(os.Args[i], "-") {
			commandIdx = i
			break
		}
	}

	// Parse global flags if any exist before the command
	if commandIdx > 1 {
		globalFlags.Parse(os.Args[1:commandIdx])
	}

	// Set paths based on data-dir flag
	dataDir = *dataDirFlag
	dbPath = dataDir + "/slab.db"
	indexPath = dataDir + "/bleve"

	command := os.Args[commandIdx]

	switch command {
	case "sync":
		runSync()
	case "search":
		// Parse search flags
		searchFlags := flag.NewFlagSet("search", flag.ExitOnError)
		semantic := searchFlags.Bool("semantic", false, "Use semantic search only")
		hybrid := searchFlags.Float64("hybrid", 0.0, "Use hybrid search (0.0-1.0, where value is semantic weight)")

		searchFlags.Parse(os.Args[commandIdx+1:])

		if searchFlags.NArg() < 1 {
			fmt.Println("Error: search query required")
			fmt.Println("Usage: slab-search [--data-dir=<dir>] search [flags] <query>")
			os.Exit(1)
		}

		query := strings.Join(searchFlags.Args(), " ")
		runSearch(query, *semantic, *hybrid)
	case "serve":
		// Parse serve flags
		serveFlags := flag.NewFlagSet("serve", flag.ExitOnError)
		port := serveFlags.String("port", "6893", "Port to listen on")
		host := serveFlags.String("host", "localhost", "Host to bind to")

		serveFlags.Parse(os.Args[commandIdx+1:])

		runServe(*host, *port)
	case "embed":
		// Parse embed flags
		embedFlags := flag.NewFlagSet("embed", flag.ExitOnError)
		startFrom := embedFlags.String("start-from", "", "Resume from document ID")

		embedFlags.Parse(os.Args[commandIdx+1:])

		runEmbed(*startFrom)
	case "reindex":
		runReindex()
	case "stats":
		runStats()
	case "get-doc":
		if len(os.Args) < commandIdx+2 {
			fmt.Println("Error: document ID required")
			fmt.Println("Usage: slab-search [--data-dir=<dir>] get-doc <document-id>")
			os.Exit(1)
		}
		runGetDoc(os.Args[commandIdx+1])
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
	fmt.Println("  slab-search [global-flags] <command> [flags]")
	fmt.Println()
	fmt.Println("Global Flags:")
	fmt.Println("  --data-dir=<dir>  Directory for database and index files (default: ./data)")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  sync                     Sync posts from Slab + generate embeddings (if Ollama running)")
	fmt.Println("  search [flags] <query>   Search for documents")
	fmt.Println("  serve [flags]            Start web server")
	fmt.Println("  embed [flags]            Generate embeddings for all documents (expensive, ~8-12 min)")
	fmt.Println("  reindex                  Rebuild Bleve keyword index (~10 seconds)")
	fmt.Println("  stats                    Show index statistics")
	fmt.Println("  get-doc <id>             Retrieve document markdown by ID")
	fmt.Println()
	fmt.Println("Search Flags:")
	fmt.Println("  -semantic         Use semantic search only (requires embeddings)")
	fmt.Println("  -hybrid=<weight>  Use hybrid search (0.0-1.0 semantic weight, default keyword-only)")
	fmt.Println()
	fmt.Println("Serve Flags:")
	fmt.Println("  -host=<host>      Host to bind to (default: localhost)")
	fmt.Println("  -port=<port>      Port to listen on (default: 6893)")
	fmt.Println()
	fmt.Println("Embed Flags:")
	fmt.Println("  -start-from=<id>  Resume from document ID (e.g., after interruption)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  slab-search sync")
	fmt.Println("  slab-search search kubernetes                    # Keyword search")
	fmt.Println("  slab-search search \"postgres config\"              # Phrase search")
	fmt.Println("  slab-search search 'deploy~'                     # Fuzzy search")
	fmt.Println("  slab-search search -semantic \"database scaling\"  # Semantic search only")
	fmt.Println("  slab-search search -hybrid=0.3 kubernetes        # Hybrid (70% keyword, 30% semantic)")
	fmt.Println("  slab-search serve                                # Start web server on http://localhost:6893")
	fmt.Println("  slab-search serve -port=3000                     # Start on custom port")
	fmt.Println("  slab-search embed                                # Generate embeddings for all documents")
	fmt.Println("  slab-search embed -start-from=abc123             # Resume from specific document ID")
	fmt.Println("  slab-search reindex                              # Rebuild Bleve index (fast)")
	fmt.Println()
	fmt.Println("Using custom data directory:")
	fmt.Println("  slab-search --data-dir=/path/to/data search kubernetes")
	fmt.Println("  slab-search --data-dir=$HOME/.slab-search serve")
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

func runGetDoc(docID string) {
	// Open database
	db, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}
	defer db.Close()

	// Retrieve document
	doc, err := db.Get(docID)
	if err != nil {
		log.Fatalf("Error retrieving document: %v", err)
	}

	if doc == nil {
		fmt.Printf("Document not found: %s\n", docID)
		os.Exit(1)
	}

	// Output markdown content
	fmt.Println(doc.Content)
}

func runEmbed(startFrom string) {
	fmt.Println("Generating embeddings for all documents...")
	fmt.Println()

	// Open database
	db, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}
	defer db.Close()

	// Initialize embeddings client
	embedder := embeddings.NewClient(ollamaURL, ollamaModel)
	if err := embedder.Health(); err != nil {
		log.Fatalf("Error: Ollama not available (%v)", err)
	}
	log.Printf("✓ Using Ollama with model: %s", ollamaModel)

	// Get all documents
	docs, err := db.List(false)
	if err != nil {
		log.Fatalf("Error listing documents: %v", err)
	}

	// Filter to resume point if specified
	startIdx := 0
	if startFrom != "" {
		found := false
		for i, doc := range docs {
			if doc.ID == startFrom {
				startIdx = i
				found = true
				fmt.Printf("Resuming from document %d/%d (ID: %s)\n", i+1, len(docs), startFrom)
				break
			}
		}
		if !found {
			log.Fatalf("Error: Document ID %s not found", startFrom)
		}
	}

	fmt.Printf("Processing %d documents (starting from index %d)\n", len(docs)-startIdx, startIdx)
	fmt.Println()
	startTime := time.Now()

	embeddingsGenerated := 0
	embeddingsFailed := 0

	for i := startIdx; i < len(docs); i++ {
		doc := docs[i]

		// Show progress every 100 documents
		if (i-startIdx) > 0 && (i-startIdx)%100 == 0 {
			percent := float64(i-startIdx) / float64(len(docs)-startIdx) * 100
			elapsed := time.Since(startTime)
			docsPerSec := float64(i-startIdx) / elapsed.Seconds()
			remaining := time.Duration(float64(len(docs)-i) / docsPerSec * float64(time.Second))

			fmt.Printf("\rProgress: %d/%d (%.1f%%) - %d generated, %d failed - ETA: %v  ",
				i-startIdx, len(docs)-startIdx, percent, embeddingsGenerated, embeddingsFailed, remaining.Round(time.Second))
		}

		// Generate embedding
		textToEmbed := fmt.Sprintf("%s\n\n%s", doc.Title, doc.Content)
		embedding, err := embedder.Embed(textToEmbed)
		if err != nil {
			log.Printf("\nWarning: Failed to generate embedding for %s (%s): %v", doc.ID, doc.Title, err)
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

	duration := time.Since(startTime)

	fmt.Printf("\rProgress: %d/%d (100.0%%) - %d generated, %d failed - Duration: %v\n",
		len(docs)-startIdx, len(docs)-startIdx, embeddingsGenerated, embeddingsFailed, duration.Round(time.Second))
	fmt.Println()
	fmt.Println("=== Embedding Generation Complete ===")
	fmt.Printf("Embeddings generated: %d\n", embeddingsGenerated)
	fmt.Printf("Failed:               %d\n", embeddingsFailed)
	fmt.Printf("Duration:             %v\n", duration.Round(time.Second))

	if embeddingsFailed > 0 {
		fmt.Println()
		fmt.Println("Note: Some embeddings failed. Check the log output above for details.")
	}
}

func runReindex() {
	fmt.Println("Rebuilding Bleve keyword search index...")
	fmt.Println()

	// Open database
	db, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}
	defer db.Close()

	// Get document count
	docs, err := db.List(false)
	if err != nil {
		log.Fatalf("Error listing documents: %v", err)
	}

	fmt.Printf("Found %d documents in database\n", len(docs))
	startTime := time.Now()

	// Open search index
	fmt.Println("Opening Bleve index...")
	idx, err := search.Open(indexPath)
	if err != nil {
		log.Fatalf("Error opening search index: %v", err)
	}
	defer idx.Close()

	// Rebuild Bleve index
	fmt.Println("Rebuilding index...")
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
	fmt.Printf("Duration:          %v\n", duration.Round(time.Second))
	fmt.Println()
	fmt.Println("Note: This only rebuilds the keyword search index.")
	fmt.Println("To generate embeddings, use: slab-search embed")
}

func runServe(host, port string) {
	log.Println("DEBUG: Starting runServe...")

	// Open database
	log.Println("DEBUG: Opening database...")
	db, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}
	defer db.Close()
	log.Println("DEBUG: Database opened")

	// Open search index
	log.Println("DEBUG: Opening search index...")
	idx, err := search.Open(indexPath)
	if err != nil {
		log.Fatalf("Error opening search index: %v", err)
	}
	defer idx.Close()
	log.Println("DEBUG: Search index opened")

	// Try to initialize embeddings client (optional)
	log.Println("DEBUG: Checking Ollama...")
	var embedder *embeddings.Client
	embedder = embeddings.NewClient(ollamaURL, ollamaModel)
	if err := embedder.Health(); err != nil {
		log.Printf("Warning: Ollama not available (%v), semantic/hybrid search disabled", err)
		log.Printf("To enable semantic search, install Ollama and run: ollama pull %s", ollamaModel)
		embedder = nil
	} else {
		log.Printf("✓ Ollama available, semantic and hybrid search enabled")
	}
	log.Println("DEBUG: Ollama check complete")

	// Create server
	log.Println("DEBUG: Creating web server...")
	server, err := web.NewServer(db, idx, embedder)
	if err != nil {
		log.Fatalf("Error creating server: %v", err)
	}
	log.Println("DEBUG: Web server created")

	addr := fmt.Sprintf("%s:%s", host, port)

	fmt.Println()
	fmt.Println("=== Slab Search Web Server ===")
	fmt.Printf("Server running at: http://%s\n", addr)
	fmt.Println()
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println()

	log.Println("DEBUG: Starting HTTP listener...")
	if err := http.ListenAndServe(addr, server.Handler()); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
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
