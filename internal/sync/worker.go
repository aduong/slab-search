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

	// 1. Fetch all topics
	log.Println("Fetching topics...")
	topics, err := w.slabClient.GetTopics(ctx)
	if err != nil {
		return nil, fmt.Errorf("get topics: %w", err)
	}
	log.Printf("Found %d topics\n", len(topics))

	// 2. Collect posts from all topics
	log.Println("Collecting posts from topics...")
	allPosts := make(map[string]*slab.SlimPost) // Use map to dedupe
	postCount := 0

	for _, topic := range topics {
		if w.maxPosts > 0 && postCount >= w.maxPosts {
			log.Printf("Reached maxPosts limit (%d), stopping topic iteration\n", w.maxPosts)
			break
		}

		posts, err := w.slabClient.GetTopicPosts(ctx, topic.ID)
		if err != nil {
			log.Printf("Warning: failed to get posts for topic %s: %v\n", topic.Name, err)
			stats.Errors++
			continue
		}

		for i := range posts {
			if w.maxPosts > 0 && postCount >= w.maxPosts {
				break
			}

			// Skip archived posts
			if posts[i].ArchivedAt != nil {
				continue
			}

			// Deduplicate (posts can appear in multiple topics)
			if _, exists := allPosts[posts[i].ID]; !exists {
				allPosts[posts[i].ID] = &posts[i]
				postCount++
			}
		}

		log.Printf("  %s: found %d posts\n", topic.Name, len(posts))
	}

	stats.TotalPosts = len(allPosts)
	log.Printf("Total unique posts to sync: %d\n", stats.TotalPosts)

	// 3. Sync each post with concurrency
	log.Println("Syncing posts...")
	postChan := make(chan *slab.SlimPost, len(allPosts))
	for _, post := range allPosts {
		postChan <- post
	}
	close(postChan)

	// Use worker pool for concurrent syncing
	concurrency := 5
	var wg sync.WaitGroup
	var mu sync.Mutex

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
	// 1. Fetch markdown content
	markdown, err := w.slabClient.GetMarkdown(ctx, slimPost.ID)
	if err != nil {
		return fmt.Errorf("get markdown: %w", err)
	}

	// 2. Compute content hash
	contentHash := fmt.Sprintf("%x", md5.Sum([]byte(markdown)))

	// 3. Check if content has changed
	existingHash, err := w.db.GetContentHash(slimPost.ID)
	if err != nil {
		return fmt.Errorf("get content hash: %w", err)
	}

	if existingHash == contentHash {
		mu.Lock()
		stats.SkippedPosts++
		mu.Unlock()
		return nil // No changes, skip
	}

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
	if existingHash == "" {
		stats.NewPosts++
	} else {
		stats.UpdatedPosts++
	}
	mu.Unlock()

	log.Printf("✓ Synced: %s\n", slimPost.Title)
	return nil
}
