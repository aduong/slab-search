package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/renderinc/slab-search/internal/embeddings"
	"github.com/renderinc/slab-search/internal/search"
	"github.com/renderinc/slab-search/internal/slab"
	"github.com/renderinc/slab-search/internal/storage"
)

// Worker handles syncing posts from Slab
type Worker struct {
	slabClient     *slab.Client
	db             *storage.DB
	index          *search.Index
	embedder       embeddings.Embedder // Optional: nil if embeddings disabled
	maxPosts       int                 // Limit for testing (0 = unlimited)
	enableEmbeddings bool              // Whether to generate embeddings
}

// NewWorker creates a new sync worker
func NewWorker(slabClient *slab.Client, db *storage.DB, index *search.Index, embedder embeddings.Embedder, maxPosts int) *Worker {
	return &Worker{
		slabClient:       slabClient,
		db:               db,
		index:            index,
		embedder:         embedder,
		maxPosts:         maxPosts,
		enableEmbeddings: embedder != nil,
	}
}

// Stats holds sync statistics
type Stats struct {
	TotalPosts       int
	NewPosts         int
	UpdatedPosts     int
	SkippedPosts     int
	ArchivedRemoved  int // Number of archived posts removed from search
	EmbeddingsGen    int // Number of embeddings generated
	EmbeddingsFailed int // Number of embedding failures
	Errors           int
	Duration         time.Duration
}

// Sync performs a full sync of posts
func (w *Worker) Sync(ctx context.Context) (*Stats, error) {
	startTime := time.Now()
	stats := &Stats{}

	log.Println("Starting sync...")

	// 1. Fetch all posts via currentSession (much faster than topic iteration)
	log.Println("Fetching all posts from Slab...")
	allPostsSlice, err := w.slabClient.GetAllSlimPosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all posts: %w", err)
	}
	log.Printf("Found %d posts from Slab\n", len(allPostsSlice))

	// 2. Filter and prepare posts, collect archived post IDs for removal
	log.Println("Filtering posts...")
	allPosts := make(map[string]*slab.SlimPost)
	archivedPostIDs := make([]string, 0)
	postCount := 0

	for i := range allPostsSlice {
		// Collect archived posts for removal from search index
		if allPostsSlice[i].ArchivedAt != nil {
			archivedPostIDs = append(archivedPostIDs, allPostsSlice[i].ID)
			continue
		}

		// Apply maxPosts limit if set (for testing)
		if w.maxPosts > 0 && postCount >= w.maxPosts {
			log.Printf("Reached maxPosts limit (%d), stopping\n", w.maxPosts)
			break
		}

		allPosts[allPostsSlice[i].ID] = &allPostsSlice[i]
		postCount++
	}

	stats.TotalPosts = len(allPosts)
	log.Printf("Total posts to sync: %d (excluding %d archived)\n", stats.TotalPosts, len(allPostsSlice)-len(allPosts))

	// 3. Sync each post with concurrency
	log.Println("Syncing posts...")
	postChan := make(chan *slab.SlimPost, len(allPosts))
	for _, post := range allPosts {
		postChan <- post
	}
	close(postChan)

	// Use worker pool for concurrent syncing
	concurrency := 20 // Increased from 5 for faster syncing
	var wg sync.WaitGroup
	var mu sync.Mutex
	var processed int

	// Progress reporting
	totalPosts := len(allPosts)
	progressTicker := time.NewTicker(5 * time.Second)
	defer progressTicker.Stop()

	go func() {
		for range progressTicker.C {
			mu.Lock()
			current := processed
			newPosts := stats.NewPosts
			updatedPosts := stats.UpdatedPosts
			skippedPosts := stats.SkippedPosts
			errors := stats.Errors
			embGen := stats.EmbeddingsGen
			mu.Unlock()
			if current > 0 && current < totalPosts {
				percent := float64(current) / float64(totalPosts) * 100
				if w.enableEmbeddings {
					log.Printf("Progress: %d/%d (%.1f%%) - %d new, %d updated, %d skipped, %d errors, %d embeddings\n",
						current, totalPosts, percent, newPosts, updatedPosts, skippedPosts, errors, embGen)
				} else {
					log.Printf("Progress: %d/%d (%.1f%%) - %d new, %d updated, %d skipped, %d errors\n",
						current, totalPosts, percent, newPosts, updatedPosts, skippedPosts, errors)
				}
			}
		}
	}()

	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for post := range postChan {
				if err := w.syncPost(ctx, post, stats, &mu); err != nil {
					log.Printf("Error syncing post %s (%s): %v\n", post.ID, post.Title, err)
					mu.Lock()
					stats.Errors++
					mu.Unlock()
				}

				// Update progress counter
				mu.Lock()
				processed++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// 4. Remove archived posts from search index
	if len(archivedPostIDs) > 0 {
		log.Printf("Removing %d archived posts from search index...\n", len(archivedPostIDs))
		for _, postID := range archivedPostIDs {
			if err := w.index.Delete(postID); err != nil {
				log.Printf("Warning: Failed to remove archived post %s from search: %v\n", postID, err)
			} else {
				stats.ArchivedRemoved++
			}
		}
		log.Printf("Removed %d archived posts from search\n", stats.ArchivedRemoved)
	}

	stats.Duration = time.Since(startTime)
	if w.enableEmbeddings {
		log.Printf("Sync complete: %d new, %d updated, %d skipped, %d archived removed, %d errors, %d embeddings generated (%d failed) in %v\n",
			stats.NewPosts, stats.UpdatedPosts, stats.SkippedPosts, stats.ArchivedRemoved, stats.Errors, stats.EmbeddingsGen, stats.EmbeddingsFailed, stats.Duration)
	} else {
		log.Printf("Sync complete: %d new, %d updated, %d skipped, %d archived removed, %d errors in %v\n",
			stats.NewPosts, stats.UpdatedPosts, stats.SkippedPosts, stats.ArchivedRemoved, stats.Errors, stats.Duration)
	}

	return stats, nil
}

// syncPost syncs a single post
func (w *Worker) syncPost(ctx context.Context, slimPost *slab.SlimPost, stats *Stats, mu *sync.Mutex) error {
	// 1. Check if post has been updated since last sync (optimization to avoid downloading markdown)
	existingUpdatedAt, err := w.db.GetUpdatedAt(slimPost.ID)
	if err != nil {
		return fmt.Errorf("get updated_at: %w", err)
	}

	// If the post exists and hasn't been updated, skip it entirely
	if !existingUpdatedAt.IsZero() && existingUpdatedAt.Equal(slimPost.UpdatedAt) {
		mu.Lock()
		stats.SkippedPosts++
		mu.Unlock()
		return nil // No changes, skip without downloading markdown
	}

	// 2. Post is new or has been updated - fetch markdown content
	markdown, err := w.slabClient.GetMarkdown(ctx, slimPost.ID)
	if err != nil {
		return fmt.Errorf("get markdown: %w", err)
	}

	// 3. Fetch full post metadata (for author info)
	post, err := w.slabClient.GetPost(ctx, slimPost.ID)
	if err != nil {
		return fmt.Errorf("get post metadata: %w", err)
	}

	// 4. Convert topics to JSON
	topicsJSON, err := json.Marshal(slimPost.Topics)
	if err != nil {
		return fmt.Errorf("marshal topics: %w", err)
	}

	// 5. Create document
	doc := &storage.Document{
		ID:          slimPost.ID,
		Title:       slimPost.Title,
		Content:     markdown,
		SlabURL:     fmt.Sprintf("https://slab.render.com/posts/%s", slimPost.ID),
		Topics:      string(topicsJSON),
		PublishedAt: slimPost.PublishedAt,
		UpdatedAt:   slimPost.UpdatedAt,
		ArchivedAt:  slimPost.ArchivedAt,
		SyncedAt:    time.Now(),
	}

	if post.Owner != nil {
		doc.AuthorName = post.Owner.Name
		doc.AuthorEmail = post.Owner.Email
	}

	// 5.5. Generate embedding if enabled (optional - graceful degradation)
	if w.enableEmbeddings {
		// Combine title and content for embedding
		textToEmbed := fmt.Sprintf("%s\n\n%s", slimPost.Title, markdown)

		embedding, err := w.embedder.Embed(textToEmbed)
		if err != nil {
			log.Printf("Warning: Failed to generate embedding for %s: %v", slimPost.ID, err)
			mu.Lock()
			stats.EmbeddingsFailed++
			mu.Unlock()
			// Continue without embedding - graceful degradation
		} else {
			doc.Embedding = embeddings.SerializeEmbedding(embedding)
			mu.Lock()
			stats.EmbeddingsGen++
			mu.Unlock()
		}
	}

	// 6. Store in database
	if err := w.db.Upsert(doc); err != nil {
		return fmt.Errorf("upsert document: %w", err)
	}

	// 7. Index in search
	var topicNames []string
	for _, t := range slimPost.Topics {
		topicNames = append(topicNames, t.Name)
	}

	indexDoc := &search.IndexedDocument{
		ID:          doc.ID,
		Title:       doc.Title,
		Content:     doc.Content,
		Author:      doc.AuthorName,
		Topics:      topicNames,
		PublishedAt: doc.PublishedAt,
		UpdatedAt:   doc.UpdatedAt,
		SlabURL:     doc.SlabURL,
	}

	if err := w.index.IndexDocument(indexDoc); err != nil {
		return fmt.Errorf("index document: %w", err)
	}

	// 8. Update stats
	mu.Lock()
	if existingUpdatedAt.IsZero() {
		stats.NewPosts++
		log.Printf("✓ New: %s\n", slimPost.Title)
	} else {
		stats.UpdatedPosts++
		log.Printf("✓ Updated: %s\n", slimPost.Title)
	}
	mu.Unlock()

	return nil
}
