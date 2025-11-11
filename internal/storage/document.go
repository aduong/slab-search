package storage

import "time"

// Document represents a document in our search index
type Document struct {
	ID            string     `db:"id"`
	Title         string     `db:"title"`
	Content       string     `db:"content"` // Markdown
	AuthorName    string     `db:"author_name"`
	AuthorEmail   string     `db:"author_email"`
	SlabURL       string     `db:"slab_url"`
	Topics        string     `db:"topics"` // JSON array
	PublishedAt   time.Time  `db:"published_at"`
	UpdatedAt     time.Time  `db:"updated_at"`
	ArchivedAt    *time.Time `db:"archived_at"` // NULL if not archived
	SyncedAt      time.Time  `db:"synced_at"`   // When we synced
	Embedding     []byte     `db:"embedding"`   // Vector embedding (BLOB) - nomic-embed-text
	EmbeddingQwen []byte     `db:"embedding_qwen"` // Qwen3 embedding for comparison
}
