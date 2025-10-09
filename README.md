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
- ✅ **Direct API discovery** via `GetAllSlimPosts()` (10,444 posts in ~3s)
- ✅ **High-performance syncing** (20 concurrent workers)
- ✅ **Timestamp-based optimization** (skips unchanged posts without downloading)
- ✅ **Incremental sync** (only downloads changed posts, 30-40x faster re-syncs)
- ✅ **Result highlighting** with `<mark>` tags
- ✅ **Progress reporting** during sync

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
# Sync all posts from Slab
./slab-search sync
```

**Sync Strategy:**
1. Fetch all posts via `currentSession.organization.posts` (~3s for 10k posts)
2. Filter out archived posts (421 archived, 10,023 active)
3. Check `updatedAt` timestamps to skip unchanged posts
4. Download markdown only for new/updated posts (20 concurrent workers)
5. Index in SQLite + Bleve with full-text search

**Performance (10,023 posts):**
- **Initial sync:** ~1m45s (fetching all markdown)
- **Re-sync (no changes):** ~2.8s (38x faster with timestamp optimization)
- **Incremental sync:** Only downloads changed posts
- **Progress reporting:** Updates every 5 seconds during sync

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
- English analyzer with stemming (find "deploy" when searching "deployment")
- Stopword removal (ignores "the", "a", "is", etc.)
- Result highlighting with context
- Shows author, URL, and score
- Sorted by relevance

**Note:** For search quality improvements and tradeoffs, see `SEARCH_IMPROVEMENTS.md`

### Reindexing

```bash
# Rebuild search index from database (no Slab sync needed)
./slab-search reindex

# Shows live progress:
# Indexing: 5000/10023 (49.9%)
```

**When to reindex:**
- After changing index configuration (analyzers, field mappings)
- When search results seem stale or incorrect
- After upgrading Bleve version
- To fix index corruption

**Performance:** ~9-10 seconds for 10,023 posts (with live progress indicator)

**Note:** This does NOT re-sync from Slab, it rebuilds the Bleve index from your existing SQLite database. Use `sync` to fetch new/updated posts from Slab.

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
- Concurrency: 20 workers
- HTTP timeout: 30 seconds
- Progress updates: Every 5 seconds

## API Details

### GraphQL Queries

**Get All Posts (Primary Method):**
```graphql
{
  currentSession {
    organization {
      posts {
        id
        title
        publishedAt
        updatedAt
        archivedAt
        topics { id }
      }
    }
  }
}
```

**Get Single Post (Metadata):**
```graphql
query GetPost($id: ID!) {
  post(id: $id) {
    id
    title
    publishedAt
    updatedAt
    archivedAt
    owner {
      id
      name
      email
    }
    topics {
      id
      name
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
2. **Direct post fetching** - `organization.posts` returns all 10k+ posts in ~3 seconds (much faster than topic iteration)
3. **Markdown export is fast** - Direct endpoint bypasses Quill Delta parsing
4. **Timestamp optimization** - Check `updatedAt` before downloading markdown (38x faster re-syncs)
5. **High concurrency works** - 20 concurrent workers for markdown fetching is optimal

## Development

### Adding Features

**Phase 2 Ideas:**
- [ ] Semantic search with embeddings
- [ ] Author/date filtering
- [ ] Web UI with HTMX
- [ ] Automated daily sync (cron/systemd timer)

**Completed:**
- [x] Incremental sync (timestamp-based optimization, 30-40x faster re-syncs)

**Considered but dropped:**
- ~~Advanced incremental sync APIs~~ - Simple `updatedAt` timestamp comparison is sufficient and very fast

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
- Check network connectivity to Slab
- Verify JWT token is valid
- Check disk space for SQLite and Bleve index

## Performance

**Measured (10,023 posts - production dataset):**
- **Initial sync:** 1m45s (~96 posts/second)
- **Re-sync (no changes):** 2.8s (38x faster with timestamp optimization)
- **Search:** <50ms for most queries
- **Index size:** ~200MB
- **Database size:** ~100MB

**Sync Breakdown:**
- API metadata fetch: ~3s (all 10,444 posts)
- Markdown downloads: ~1m40s (20 concurrent workers)
- Indexing: Overlapped with downloads
- Timestamp checks: ~2ms per post (10k posts in 2.8s total)

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
