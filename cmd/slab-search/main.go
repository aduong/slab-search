package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/renderinc/slab-search/internal/search"
	"github.com/renderinc/slab-search/internal/slab"
	"github.com/renderinc/slab-search/internal/storage"
	"github.com/renderinc/slab-search/internal/sync"
)

const (
	dataDir   = "./data"
	dbPath    = "./data/slab.db"
	indexPath = "./data/bleve"
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
		if len(os.Args) < 3 {
			fmt.Println("Error: search query required")
			fmt.Println("Usage: slab-search search <query>")
			os.Exit(1)
		}
		query := strings.Join(os.Args[2:], " ")
		runSearch(query)
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
	fmt.Println("  slab-search sync              Sync posts from Slab")
	fmt.Println("  slab-search search <query>    Search for documents")
	fmt.Println("  slab-search stats             Show index statistics")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  slab-search sync")
	fmt.Println("  slab-search search kubernetes")
	fmt.Println("  slab-search search \"postgres config\"")
	fmt.Println("  slab-search search 'deploy~'  # Fuzzy search")
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

	// Create sync worker (limit to 10 posts for MVP testing)
	worker := sync.NewWorker(slabClient, db, idx, 10)

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
	fmt.Printf("Errors:        %d\n", stats.Errors)
	fmt.Printf("Duration:      %v\n", stats.Duration)
}

func runSearch(query string) {
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

	// Perform search
	results, err := idx.Search(query, 10)
	if err != nil {
		log.Fatalf("Error searching: %v", err)
	}

	// Display results
	if len(results) == 0 {
		fmt.Println("No results found")
		return
	}

	fmt.Printf("Found %d results:\n\n", len(results))

	for i, result := range results {
		fmt.Printf("%d. %s\n", i+1, result.Title)
		if result.Author != "" {
			fmt.Printf("   Author: %s\n", result.Author)
		}
		fmt.Printf("   URL: %s\n", result.SlabURL)
		fmt.Printf("   Score: %.2f\n", result.Score)

		// Show content snippets if available
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
