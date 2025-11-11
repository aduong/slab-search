package storage

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps SQLite database operations
type DB struct {
	db *sql.DB
}

// Open opens or creates a SQLite database
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Enable foreign keys and WAL mode for better concurrency
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	storage := &DB{db: db}

	// Initialize schema
	if err := storage.initSchema(); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return storage, nil
}

// Close closes the database
func (d *DB) Close() error {
	return d.db.Close()
}

// initSchema creates tables if they don't exist and handles migrations
func (d *DB) initSchema() error {
	// Create base table
	schema := `
	CREATE TABLE IF NOT EXISTS documents (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		content TEXT NOT NULL,
		author_name TEXT,
		author_email TEXT,
		slab_url TEXT NOT NULL,
		topics TEXT,
		published_at TIMESTAMP,
		updated_at TIMESTAMP,
		archived_at TIMESTAMP,
		synced_at TIMESTAMP NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_author ON documents(author_name);
	CREATE INDEX IF NOT EXISTS idx_published ON documents(published_at);
	CREATE INDEX IF NOT EXISTS idx_updated ON documents(updated_at);
	CREATE INDEX IF NOT EXISTS idx_archived ON documents(archived_at);
	CREATE INDEX IF NOT EXISTS idx_synced ON documents(synced_at);
	`

	if _, err := d.db.Exec(schema); err != nil {
		return err
	}

	// Run migrations
	return d.runMigrations()
}

// runMigrations handles schema migrations for existing databases
func (d *DB) runMigrations() error {
	// Migration 1: Add embedding column (Phase 2 - Semantic Search)
	// Check if embedding column exists
	var columnExists bool
	err := d.db.QueryRow(`
		SELECT COUNT(*) > 0
		FROM pragma_table_info('documents')
		WHERE name='embedding'
	`).Scan(&columnExists)

	if err != nil {
		return fmt.Errorf("check embedding column: %w", err)
	}

	if !columnExists {
		// Add embedding column
		_, err = d.db.Exec("ALTER TABLE documents ADD COLUMN embedding BLOB")
		if err != nil {
			return fmt.Errorf("add embedding column: %w", err)
		}
	}

	// Migration 2: Add embedding_qwen column (for model comparison)
	var qwenColumnExists bool
	err = d.db.QueryRow(`
		SELECT COUNT(*) > 0
		FROM pragma_table_info('documents')
		WHERE name='embedding_qwen'
	`).Scan(&qwenColumnExists)

	if err != nil {
		return fmt.Errorf("check embedding_qwen column: %w", err)
	}

	if !qwenColumnExists {
		// Add embedding_qwen column
		_, err = d.db.Exec("ALTER TABLE documents ADD COLUMN embedding_qwen BLOB")
		if err != nil {
			return fmt.Errorf("add embedding_qwen column: %w", err)
		}
	}

	return nil
}

// Upsert inserts or updates a document
func (d *DB) Upsert(doc *Document) error {
	query := `
	INSERT INTO documents (
		id, title, content, author_name, author_email,
		slab_url, topics, published_at, updated_at, archived_at, synced_at, embedding, embedding_qwen
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		title = excluded.title,
		content = excluded.content,
		author_name = excluded.author_name,
		author_email = excluded.author_email,
		slab_url = excluded.slab_url,
		topics = excluded.topics,
		published_at = excluded.published_at,
		updated_at = excluded.updated_at,
		archived_at = excluded.archived_at,
		synced_at = excluded.synced_at,
		embedding = excluded.embedding,
		embedding_qwen = excluded.embedding_qwen
	`

	_, err := d.db.Exec(query,
		doc.ID, doc.Title, doc.Content, doc.AuthorName, doc.AuthorEmail,
		doc.SlabURL, doc.Topics, doc.PublishedAt, doc.UpdatedAt, doc.ArchivedAt, doc.SyncedAt, doc.Embedding, doc.EmbeddingQwen,
	)
	return err
}

// Get retrieves a document by ID
func (d *DB) Get(id string) (*Document, error) {
	doc := &Document{}
	query := `
	SELECT id, title, content, author_name, author_email,
	       slab_url, topics, published_at, updated_at, archived_at, synced_at, embedding, embedding_qwen
	FROM documents
	WHERE id = ?
	`

	err := d.db.QueryRow(query, id).Scan(
		&doc.ID, &doc.Title, &doc.Content, &doc.AuthorName, &doc.AuthorEmail,
		&doc.SlabURL, &doc.Topics, &doc.PublishedAt, &doc.UpdatedAt, &doc.ArchivedAt, &doc.SyncedAt, &doc.Embedding, &doc.EmbeddingQwen,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return doc, nil
}

// List retrieves all documents (non-archived by default)
func (d *DB) List(includeArchived bool) ([]*Document, error) {
	query := `
	SELECT id, title, content, author_name, author_email,
	       slab_url, topics, published_at, updated_at, archived_at, synced_at, embedding, embedding_qwen
	FROM documents
	`
	if !includeArchived {
		query += " WHERE archived_at IS NULL"
	}
	query += " ORDER BY updated_at DESC"

	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []*Document
	for rows.Next() {
		doc := &Document{}
		err := rows.Scan(
			&doc.ID, &doc.Title, &doc.Content, &doc.AuthorName, &doc.AuthorEmail,
			&doc.SlabURL, &doc.Topics, &doc.PublishedAt, &doc.UpdatedAt, &doc.ArchivedAt, &doc.SyncedAt, &doc.Embedding, &doc.EmbeddingQwen,
		)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}

	return docs, rows.Err()
}

// Count returns the total number of documents
func (d *DB) Count() (int, error) {
	var count int
	err := d.db.QueryRow("SELECT COUNT(*) FROM documents WHERE archived_at IS NULL").Scan(&count)
	return count, err
}

// GetUpdatedAt retrieves just the updated_at timestamp for a document
// Returns zero time if document doesn't exist
func (d *DB) GetUpdatedAt(id string) (time.Time, error) {
	var updatedAt time.Time
	err := d.db.QueryRow("SELECT updated_at FROM documents WHERE id = ?", id).Scan(&updatedAt)
	if err == sql.ErrNoRows {
		return time.Time{}, nil // Return zero time for non-existent docs
	}
	return updatedAt, err
}
