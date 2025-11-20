# Slab Search

Fast search for Slab documents with keyword, semantic, and hybrid search modes. Includes both CLI and web interface.

## Quick Start

```bash
# Build
go build -o slab-search ./cmd/slab-search

# Sync posts from Slab (requires JWT token)
export SLAB_TOKEN="your-jwt-token"
./slab-search sync

# Option 1: Web Interface (recommended)
./slab-search serve
# Open http://localhost:6893 in your browser

# Option 2: CLI Search
./slab-search search "kubernetes"
./slab-search search "postgres config"
./slab-search search "deploy~"  # Fuzzy search

# Optional: Generate embeddings for semantic search
./slab-search embed  # Takes ~8-12 minutes for 10k docs
```

## Features

### Search
- ✅ **Keyword search** with Bleve (fuzzy matching, phrase search)
- ✅ **Semantic search** with Ollama embeddings (conceptual matching)
- ✅ **Hybrid search** combining keyword + semantic (70/30 split)
- ✅ **Web UI** with real-time search and clickable results
- ✅ **CLI search** for command-line use
- ✅ **Result highlighting** with `<mark>` tags

### Data Management
- ✅ **High-performance syncing** (20 concurrent workers, ~1m45s for 10k posts)
- ✅ **Incremental sync** (only downloads changed posts, 30-40x faster re-syncs)
- ✅ **Fast reindexing** (~10 seconds for Bleve index rebuild)
- ✅ **Resumable embedding generation** (can restart from specific document ID)
- ✅ **Progress reporting** during sync and embedding generation

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

### Web Interface (Recommended)

```bash
# Start the web server
./slab-search serve

# Custom port
./slab-search serve -port=3000

# Open in browser
# http://localhost:6893
```

**Web UI Features:**
- Real-time search with 300ms debounce
- Toggle between keyword, hybrid (70/30), and semantic search
- Clickable results that open Slab posts in new tabs
- Result previews with highlighted matches
- Keyboard shortcut: Press `/` to focus search
- Mobile responsive design

See `WEB_FRONTEND.md` for implementation details.

### CLI Search

```bash
# Keyword search (default)
./slab-search search kubernetes
./slab-search search "postgres config"  # Phrase search
./slab-search search "deployement~"     # Fuzzy search

# Semantic search (requires embeddings)
./slab-search search -semantic "database scaling"

# Hybrid search (70% keyword, 30% semantic)
./slab-search search -hybrid=0.3 kubernetes
```

**Search Features:**
- **Title boosting**: Documents with matches in title rank 3x higher
- **English analyzer** with stemming (find "deploy" when searching "deployment")
- **Stopword removal** (ignores "the", "a", "is", etc.)
- **Result highlighting** with context snippets
- Shows author, URL, and relevance score
- Sorted by relevance

**Note:** For search quality improvements and implementation details, see `SEARCH_IMPROVEMENTS.md`

### Generating Embeddings (Optional)

Embeddings enable semantic and hybrid search modes. This is optional but recommended for better search quality.

```bash
# Generate embeddings for all documents (requires Ollama)
./slab-search embed

# Resume from a specific document (if interrupted)
./slab-search embed -start-from=abc123xyz
```

**Prerequisites:**
```bash
# Install Ollama
curl -fsSL https://ollama.com/install.sh | sh

# Pull the embedding model
ollama pull nomic-embed-text
```

**Performance:**
- ~8-12 minutes for 10,023 posts
- Progress updates every 100 documents
- Shows ETA and failure count
- Can be interrupted and resumed

**When to run:**
- After initial sync to enable semantic/hybrid search
- When you want to use semantic search features
- After upgrading the embedding model

### Reindexing

```bash
# Rebuild Bleve keyword search index (fast, no embeddings)
./slab-search reindex
```

**Performance:**
- ~10 seconds for 10,023 posts
- Does NOT regenerate embeddings (use `embed` command for that)

**When to reindex:**
- After changing index configuration (analyzers, field mappings)
- When keyword search results seem stale or incorrect
- After upgrading Bleve version
- To fix index corruption

**Note:** The `reindex` and `embed` commands are now separate. This allows you to:
- Run `serve` while `embed` is generating embeddings (Bleve index not locked)
- Rebuild the keyword index quickly without regenerating embeddings
- Resume embedding generation if interrupted

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
│  1. Fetch all post metadata         │
│  2. Download markdown (concurrent)  │
│  3. Generate embeddings (Ollama)    │
│  4. Store & index                   │
└───────┬──────────────────┬──────────┘
        │                  │
        ▼                  ▼
┌──────────────┐  ┌─────────────────┐
│   SQLite     │  │  Bleve Index    │
│  • Metadata  │  │  • Keyword      │
│  • Content   │  │  • Fuzzy match  │
│  • Embeddings│  │  • Highlighting │
└──────┬───────┘  └────────┬────────┘
       │                   │
       └──────────┬────────┘
                  │
       ┌──────────▼──────────┐
       │   Search Layer      │
       │  • Keyword          │
       │  • Semantic         │
       │  • Hybrid (70/30)   │
       └──────────┬──────────┘
                  │
         ┌────────┴────────┐
         │                 │
         ▼                 ▼
    ┌─────────┐      ┌──────────┐
    │ Web UI  │      │ CLI      │
    │ (HTMX)  │      │ Search   │
    └─────────┘      └──────────┘
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
│   │   ├── index.go         # Bleve keyword search
│   │   └── semantic.go      # Semantic search (embeddings)
│   ├── embeddings/
│   │   └── ollama.go        # Ollama embedding client
│   ├── sync/
│   │   └── worker.go        # Concurrent sync worker
│   └── web/
│       ├── server.go        # HTTP server & handlers
│       ├── templates/
│       │   └── index.html   # Search UI template
│       └── static/
│           └── style.css    # Styling
├── data/                    # Created at runtime
│   ├── slab.db             # SQLite database
│   └── bleve/              # Search index
├── design.md               # Design document
├── WEB_FRONTEND.md         # Web UI implementation guide
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

**Completed:**
- [x] Incremental sync (timestamp-based optimization, 30-40x faster re-syncs)
- [x] Semantic search with embeddings (Ollama + nomic-embed-text)
- [x] Hybrid search (keyword + semantic, 70/30 split)
- [x] Web UI with HTMX (real-time search, clickable results)
- [x] Separate embed command (resumable, doesn't lock Bleve index)

**Phase 3 Ideas:**
- [ ] Author/date filtering in web UI
- [ ] Automated daily sync (cron/systemd timer)
- [ ] Search analytics and popular queries
- [ ] Saved searches

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
- **Embedding generation:** 8-12 minutes (~14 docs/second)
- **Reindex (Bleve only):** ~10 seconds
- **Keyword search:** <50ms for most queries
- **Semantic search:** ~40ms for 10k documents (brute-force cosine similarity)
- **Index size:** ~200MB
- **Database size:** ~100MB (~130MB with embeddings)

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

## Credits

Built with Claude Code during API exploration and rapid prototyping session.
