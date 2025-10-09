package sync

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/renderinc/slab-search/internal/search"
	"github.com/renderinc/slab-search/internal/slab"
	"github.com/renderinc/slab-search/internal/storage"
)

// Worker handles syncing posts from Slab
type Worker struct {
	slabClient *slab.Client
	db         *storage.DB
	index      *search.Index
	maxPosts   int // Limit for testing (0 = unlimited)
}

// NewWorker creates a new sync worker
func NewWorker(slabClient *slab.Client, db *storage.DB, index *search.Index, maxPosts int) *Worker {
	return &Worker{
		slabClient: slabClient,
		db:         db,
		index:      index,
		maxPosts:   maxPosts,
	}
}

// Stats holds sync statistics
type Stats struct {
	TotalPosts   int
	NewPosts     int
	UpdatedPosts int
	SkippedPosts int
	Errors       int
	Duration     time.Duration
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

	// 2. Filter and prepare posts
	log.Println("Filtering posts...")
	allPosts := make(map[string]*slab.SlimPost)
	postCount := 0

	for i := range allPostsSlice {
		// Skip archived posts
		if allPostsSlice[i].ArchivedAt != nil {
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
			mu.Unlock()
			if current > 0 && current < totalPosts {
				percent := float64(current) / float64(totalPosts) * 100
				log.Printf("Progress: %d/%d (%.1f%%) - %d new, %d updated, %d skipped, %d errors\n",
					current, totalPosts, percent, stats.NewPosts, stats.UpdatedPosts, stats.SkippedPosts, stats.Errors)
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

	stats.Duration = time.Since(startTime)
	log.Printf("Sync complete: %d new, %d updated, %d skipped, %d errors in %v\n",
		stats.NewPosts, stats.UpdatedPosts, stats.SkippedPosts, stats.Errors, stats.Duration)

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

	// 3. Compute content hash
	contentHash := fmt.Sprintf("%x", md5.Sum([]byte(markdown)))

	// 4. Fetch full post metadata (for author info)
	post, err := w.slabClient.GetPost(ctx, slimPost.ID)
	if err != nil {
		return fmt.Errorf("get post metadata: %w", err)
	}

	// 5. Convert topics to JSON
	topicsJSON, err := json.Marshal(slimPost.Topics)
	if err != nil {
		return fmt.Errorf("marshal topics: %w", err)
	}

	// 6. Create document
	doc := &storage.Document{
		ID:          slimPost.ID,
		Title:       slimPost.Title,
		Content:     markdown,
		ContentHash: contentHash,
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

	// 7. Store in database
	if err := w.db.Upsert(doc); err != nil {
		return fmt.Errorf("upsert document: %w", err)
	}

	// 8. Index in search
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

	// 9. Update stats
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
