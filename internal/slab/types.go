package slab

import "time"

// Topic represents a Slab topic/collection
type Topic struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Post represents a Slab post with metadata
type Post struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	PublishedAt time.Time `json:"publishedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	ArchivedAt  *time.Time `json:"archivedAt"` // nil if not archived
	Owner       *User      `json:"owner"`
	Topics      []Topic    `json:"topics"`
}

// User represents a Slab user
type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// SlimPost is the lightweight post format from organization.topics.posts
type SlimPost struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	PublishedAt time.Time  `json:"publishedAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	ArchivedAt  *time.Time `json:"archivedAt"`
	Topics      []Topic    `json:"topics"`
}
