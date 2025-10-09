# Slab Search

Fast, local search for Slab documents with fuzzy matching and markdown indexing.

## Quick Start

```bash
# Build
go build -o slab-search ./cmd/slab-search

# Sync posts from Slab (requires JWT token)
export SLAB_TOKEN="your-jwt-token"
./slab-search sync

# Search
./slab-search search "kubernetes"
./slab-search search "postgres config"
./slab-search search "deploy~"  # Fuzzy search

# Stats
./slab-search stats
```

## Features

- ✅ **Full-text search** with Bleve
- ✅ **Fuzzy matching** for typos (use `~` suffix)
- ✅ **Phrase search** with quotes
- ✅ **Markdown indexing** from Slab export API
- ✅ **Topic-based discovery** (1081 topics)
- ✅ **Concurrent syncing** (5 workers)
- ✅ **Change detection** via MD5 hashing
- ✅ **Result highlighting** with `<mark>` tags

## Installation

### Prerequisites

- Go 1.21+
- SQLite3
- Slab JWT token (see [Getting a Token](#getting-a-token))

### Build from Source

```bash
git clone <repo-url>
cd slab-search
go mod download
go build -o slab-search ./cmd/slab-search
```

## Usage

### Authentication

Create a `token` file or set the `SLAB_TOKEN` environment variable:

```bash
# Option 1: Token file
echo "your-jwt-token-here" > token

# Option 2: Environment variable
export SLAB_TOKEN="your-jwt-token-here"
```

### Syncing

```bash
# Initial sync (limited to 10 posts by default for testing)
./slab-search sync

# Edit cmd/slab-search/main.go to remove the limit:
# Change: worker := sync.NewWorker(slabClient, db, idx, 10)
# To:     worker := sync.NewWorker(slabClient, db, idx, 0)
```

**Sync Strategy:**
1. Fetches all topics from Slab (1081 topics)
2. Iterates through topics to discover posts
3. Fetches markdown content concurrently
4. Indexes in SQLite + Bleve
5. Skips unchanged posts (MD5 hash comparison)

**Performance:**
- 10 posts: ~1 second
- 100 posts: ~10-20 seconds (estimated)
- 1000 posts: ~2-3 minutes (estimated)

### Searching

```bash
# Basic search
./slab-search search kubernetes

# Phrase search
./slab-search search "postgres config"

# Fuzzy search (finds typos)
./slab-search search "deployement~"

# Multiple terms
./slab-search search "database redis cache"
```

**Search Features:**
- Title matches boosted 3x
- Result highlighting with context
- Shows author, URL, and score
- Sorted by relevance

### Statistics

```bash
./slab-search stats
```

Shows document counts in database and search index.

## Architecture

```
┌─────────────────────────────────────┐
│       Slab API (render.com)         │
│  • GraphQL (currentSession)         │
│  • Markdown Export                  │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│         Sync Worker                 │
│  1. Get topics                      │
│  2. Get posts per topic             │
│  3. Fetch markdown (concurrent)     │
│  4. Hash & detect changes           │
│  5. Store & index                   │
└───────┬─────────────────┬───────────┘
        │                 │
        ▼                 ▼
┌──────────────┐  ┌──────────────────┐
│   SQLite     │  │  Bleve Index     │
│  (metadata)  │  │  (full-text)     │
└──────────────┘  └──────────────────┘
        ▲                 ▲
        │                 │
        └────────┬────────┘
                 │
        ┌────────▼────────┐
        │  Search CLI     │
        └─────────────────┘
```

## Project Structure

```
slab-search/
├── cmd/slab-search/
│   └── main.go              # CLI entry point
├── internal/
│   ├── slab/
│   │   ├── client.go        # GraphQL + HTTP client
│   │   └── types.go         # Data models
│   ├── storage/
│   │   ├── db.go            # SQLite operations
│   │   └── document.go      # Document model
│   ├── search/
│   │   └── index.go         # Bleve search index
│   └── sync/
│       └── worker.go        # Concurrent sync worker
├── data/                    # Created at runtime
│   ├── slab.db             # SQLite database
│   └── bleve/              # Search index
├── design.md               # Design document
├── API_FINDINGS.md         # API exploration notes
└── README.md               # This file
```

## Configuration

Currently uses hardcoded values. Future: `config.yaml` support.

**Defaults:**
- Data directory: `./data`
- Database: `./data/slab.db`
- Index: `./data/bleve`
- Concurrency: 5 workers
- Topic posts limit: 100 per topic
- HTTP timeout: 30 seconds

## API Details

### GraphQL Queries

**Get Topics:**
```graphql
{
  currentSession {
    organization {
      topics {
        id
        name
      }
    }
  }
}
```

**Get Posts for Topic:**
```graphql
query GetTopicPosts($topicId: ID!, $first: Int) {
  topic(id: $topicId) {
    posts(first: $first) {
      edges {
        node {
          id
          title
          publishedAt
          updatedAt
          archivedAt
          topics { id name }
        }
      }
    }
  }
}
```

**Markdown Export:**
```
GET https://slab.render.com/posts/{id}/export/markdown
Authorization: Bearer {jwt_token}
```

### Key Discoveries

1. **Use `currentSession`** - JWT tokens provide viewer access; must use `currentSession` to get full Organization type
2. **Connections require pagination** - Topic posts use connection pattern; must specify `first` or `last`
3. **Markdown export is fast** - Direct endpoint bypasses Quill Delta parsing
4. **1081 topics** - Large organizations need efficient iteration

## Development

### Adding Features

**Phase 2 Ideas:**
- [ ] Semantic search with embeddings
- [ ] Author/date filtering
- [ ] Web UI with HTMX
- [ ] Automated daily sync
- [ ] Incremental sync optimization
- [ ] Pagination for topics with >100 posts

### Testing

```bash
# Build
go build -o slab-search ./cmd/slab-search

# Run with test limit (10 posts)
./slab-search sync

# Test search
./slab-search search "test query"

# Check data
sqlite3 data/slab.db "SELECT COUNT(*) FROM documents;"
```

### Dependencies

```go
require (
    github.com/blevesearch/bleve/v2  // Search engine
    github.com/mattn/go-sqlite3      // SQLite driver
)
```

## Troubleshooting

### "Error reading token file"
Create a `token` file with your JWT or set `SLAB_TOKEN` environment variable.

### "Cannot query field topics"
Make sure queries use `currentSession { organization { ... } }` pattern.

### "You must either supply :first or :last"
Connection queries require pagination. All implemented queries include `first: 100`.

### Slow sync
- Check concurrency setting (default: 5 workers)
- Verify network connectivity
- Consider pagination if topics have >100 posts

## Performance

**Measured (10 posts):**
- Sync: 1.03 seconds
- Search: <50ms
- Index size: ~2MB

**Estimated (1000 posts):**
- Sync: 2-3 minutes (first time)
- Re-sync: <1 minute (with hash detection)
- Index size: ~200MB
- Search: <100ms

## Getting a Token

1. Log into Slab in your browser
2. Open Developer Tools → Network tab
3. Navigate to any Slab page
4. Find a GraphQL request
5. Copy the `Authorization: Bearer <token>` value
6. Save to `token` file or `SLAB_TOKEN` env var

**Note:** JWT tokens may expire. Refresh by re-extracting from browser.

## License

Internal Render tool.

## Credits

Built with Claude Code during API exploration and rapid prototyping session.
