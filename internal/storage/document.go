package storage

import "time"

// Document represents a document in our search index
type Document struct {
	ID          string    `db:"id"`
	Title       string    `db:"title"`
	Content     string    `db:"content"`      // Markdown
	ContentHash string    `db:"content_hash"` // MD5 of content
	AuthorName  string    `db:"author_name"`
	AuthorEmail string    `db:"author_email"`
	SlabURL     string    `db:"slab_url"`
	Topics      string    `db:"topics"` // JSON array
	PublishedAt time.Time `db:"published_at"`
	UpdatedAt   time.Time `db:"updated_at"`
	ArchivedAt  *time.Time `db:"archived_at"` // NULL if not archived
	SyncedAt    time.Time  `db:"synced_at"`   // When we synced
}
