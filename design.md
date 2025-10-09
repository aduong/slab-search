# Slab Search Enhancement - Project Specification

## ✅ IMPLEMENTATION STATUS

**Phase 1 MVP: COMPLETE + OPTIMIZED** (2025-10-08/09)
**Phase 2 Semantic Search: IN PROGRESS** (2025-10-09)

**What Works (Phase 1):**
- ✅ Direct post discovery via `GetAllSlimPosts()` (10,444 posts in ~3s)
- ✅ High-performance concurrent markdown fetching (20 workers)
- ✅ Timestamp-based optimization (38x faster re-syncs)
- ✅ SQLite storage with change detection
- ✅ Bleve full-text search with fuzzy matching
- ✅ CLI commands: sync, search, reindex, stats
- ✅ Reindex without re-syncing from Slab (~8s for 10k posts)
- ✅ Progress reporting during sync (every 5 seconds)
- ✅ Full dataset: 10,023 posts synced in 1m45s (initial) / 2.8s (re-sync)

**In Progress (Phase 2):**
- 🔄 Semantic search with Ollama embeddings
- 🔄 Hybrid scoring (keyword + semantic)
- See `EMBEDDINGS_IMPLEMENTATION.md` for implementation plan

**Key Learnings:**
- `currentSession.organization.posts` is much faster than topic iteration
- `updatedAt` timestamp comparison avoids unnecessary markdown downloads (no content hash needed)
- 20 concurrent workers is optimal for I/O-bound markdown fetching
- Markdown export endpoint works perfectly
- Simple timestamp-based incremental sync is sufficient (no need for complex delta APIs)
- Timestamp checking achieves 30-40x faster re-syncs

See `README.md` for usage and `API_FINDINGS.md` for implementation details.

---

## 1. Executive Summary

### Problem Statement
Slab's native search functionality is inadequate for finding relevant documents quickly, reducing the value of our knowledge base. With approximately 10,000 documents accumulated over 7 years, employees struggle to locate important information, leading to duplicated work and lost institutional knowledge.

### Proposed Solution
Build a custom search layer that periodically syncs documents from Slab and provides fast, accurate full-text search with fuzzy matching and phrase search capabilities. The system will start as a local tool and can evolve into a hosted service shared across the organization.

### Success Criteria
- Search results return in under 1 second for 95% of queries
- Relevant documents appear in top 5 results for 80% of searches
- System successfully indexes 100% of accessible Slab documents
- Daily sync completes within 30 minutes

## 2. Product Requirements

### 2.1 User Stories

**As a team member, I want to:**
- Search for documents containing specific phrases or keywords
- Find documents even when I misspell search terms
- Filter results by author when I know who wrote something
- See a preview of matching content to assess relevance
- Access the original document in Slab with one click
- Search for recent documents by date range

### 2.2 Functional Requirements

#### Core Features (MVP)
- **Full-text search**: Search across document titles and content
- **Fuzzy matching**: Find results despite typos (e.g., "deployement" finds "deployment")
- **Phrase search**: Search for exact phrases using quotes
- **Semantic search**: Find conceptually related content using embeddings
- **Hybrid scoring**: Combine keyword and semantic search for best results
- **Result preview**: Display document title, author, date, and text snippet with highlighted matches
- **Direct linking**: Each result links to the original Slab document
- **Daily sync**: Automatic synchronization of all accessible documents

#### Enhanced Features (Phase 2)
- **Author filtering**: Narrow results by document author
- **Date filtering**: Filter by publish or update date ranges
- **Incremental sync**: Only sync changed documents for efficiency
- **Topic filtering**: Filter by Slab topics/tags
- **Performance optimizations**: Add sqlite-vss if needed for vector search

#### Future Considerations (Phase 3)
- **Search analytics**: Track popular searches and click-through rates
- **Saved searches**: Allow users to save and share search queries
- **API access**: Enable programmatic search for integrations

### 2.3 Non-Functional Requirements

#### Performance
- Search response time: < 1 second for 95% of queries
- Sync performance: Complete daily sync in < 30 minutes
- Storage efficiency: Compress markdown content (target < 1GB for 10,000 docs)
- Concurrent users: Support 10 simultaneous searches (local), 50 (hosted)

#### Reliability
- Sync resilience: Continue operation if sync fails, use last known good data
- Error handling: Graceful degradation when Slab API is unavailable
- Data consistency: Verify document integrity after sync

#### Usability
- Zero configuration for end users
- Self-explanatory search interface
- Mobile-responsive web design
- Keyboard shortcuts for power users (e.g., `/` to focus search)

#### Security
- Respect Slab access controls (only index public/accessible documents)
- No storage of sensitive credentials in code
- Optional authentication for hosted version

## 3. Technical Specification

### 3.1 Architecture Overview

```
┌───────────────────────────────────────────────────────────┐
│                    Slab API                               │
│  • GraphQL: https://slab.render.com/graphql               │
│  • Markdown Export: /posts/{id}/export/markdown           │
└───────────────────────┬───────────────────────────────────┘
                        │
                        │ Daily Sync (Cron/Scheduler)
                        ▼
┌───────────────────────────────────────────────────────────┐
│                   Sync Worker                             │
│  1. Search posts via GraphQL (discover IDs)               │
│  2. Fetch metadata via GraphQL (batched)                  │
│  3. Download markdown via HTTP (concurrent)               │
│  4. Compare content hashes for changes                    │
│  5. Update database and search index                      │
└──────────────┬─────────────────────┬──────────────────────┘
               │                     │
               ▼                     ▼
    ┌──────────────────┐  ┌──────────────────────┐
    │   SQLite DB      │  │  Search Index        │
    │                  │  │    (Bleve)           │
    │ • Metadata       │  │                      │
    │ • Content hashes │  │ • Full-text          │
    │ • Markdown       │  │ • Fuzzy match        │
    │ • Embeddings     │  │ • Phrase search      │
    │ • Sync state     │  │ • Semantic (hybrid)  │
    └──────────────────┘  └──────────────────────┘
               ▲                     ▲
               │                     │
               └─────────┬───────────┘
                         │
┌───────────────────────────────────────────────────────────┐
│                  Web Server (Go)                          │
│  • Search API endpoint (keyword + semantic)               │
│  • Web UI (Go templates + HTMX)                           │
│  • Static asset serving                                   │
└───────────────────────────────────────────────────────────┘
                         ▲
                         │
                    [Web Browser]
```

### 3.2 Technology Stack

#### Core Technologies
- **Language**: Go 1.21+
- **Search Index**: Bleve (native Go, no dependencies)
- **Database**: SQLite (metadata and content storage)
- **Web Framework**: Standard library net/http + Chi router
- **UI**: Go templates + HTMX for interactivity
- **API Client**:
  - GraphQL: Standard net/http (simple POST requests)
  - Markdown: Standard net/http GET with JWT auth

#### Development Tools
- **Configuration**: Viper for config management
- **Logging**: Zerolog or Zap for structured logging
- **Testing**: Standard library + Testify for assertions
- **Migration**: golang-migrate for database schemas

### 3.3 Data Models

#### SQLite Schema
```sql
-- Single documents table (all we need for MVP)
CREATE TABLE documents (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    content TEXT NOT NULL,        -- Markdown from export endpoint
    author_name TEXT,
    author_email TEXT,
    slab_url TEXT NOT NULL,       -- https://slab.render.com/posts/{id}
    topics TEXT,                  -- JSON array of {id, name}
    published_at TIMESTAMP,
    updated_at TIMESTAMP,
    archived_at TIMESTAMP,        -- NULL if not archived
    synced_at TIMESTAMP NOT NULL, -- When we last synced this doc
    embedding BLOB                -- Phase 2: 768 floats × 4 bytes = 3KB per doc
);

-- Indexes for common query patterns
CREATE INDEX idx_author ON documents(author_name);
CREATE INDEX idx_published ON documents(published_at);
CREATE INDEX idx_updated ON documents(updated_at);
CREATE INDEX idx_archived ON documents(archived_at);
CREATE INDEX idx_synced ON documents(synced_at);
```

#### Sync State (JSON file)
```json
// data/sync_state.json
{
  "last_sync_at": "2024-01-20T02:00:00Z",
  "last_success_at": "2024-01-20T02:00:00Z",
  "documents_synced": 9847,
  "sync_duration_ms": 124000,
  "error": null,
  "partial_progress": {
    "last_processed_id": "doc_xyz123",
    "processed_count": 5000
  }
}
```

#### Bleve Index Structure
```go
type IndexedDocument struct {
    ID          string
    Title       string    // Boosted field
    Content     string    // Main search field
    Author      string    // Facet field
    Topics      []string  // Facet field
    PublishDate time.Time // Date range field
    UpdateDate  time.Time // Date range field
}
```

#### Search Implementation (MVP)
```go
// Bleve index configuration
type SearchIndex struct {
    index bleve.Index
}

// Document structure for indexing
type IndexedDocument struct {
    ID          string
    Title       string    // Boosted field (weight: 3.0)
    Content     string    // Standard field
    Author      string    // Facet field
    Topics      []string  // Facet field
    PublishDate time.Time
    UpdateDate  time.Time
}

// Query with fuzzy matching
func (s *SearchIndex) Search(query string, limit int) ([]Result, error) {
    q := bleve.NewQueryStringQuery(query)
    req := bleve.NewSearchRequest(q)
    req.Size = limit
    req.Highlight = bleve.NewHighlight()
    return s.index.Search(req)
}
```

#### Semantic Search (Phase 2)
```go
// Implementation using Ollama for embedding generation
// See EMBEDDINGS_IMPLEMENTATION.md for full design and rationale

type EmbeddingClient struct {
    baseURL string // http://localhost:11434
    model   string // nomic-embed-text (768-dim)
}

func (c *EmbeddingClient) Embed(text string) ([]float32, error) {
    // POST to Ollama API: /api/embed
}

// Semantic search using brute-force cosine similarity
// Performance: ~40ms for 10k documents (acceptable for our scale)
func SemanticSearch(queryEmbedding []float32, limit int) []*SearchResult {
    // 1. Load all document embeddings from SQLite
    // 2. Compute cosine similarity: dot(a,b) / (norm(a) * norm(b))
    // 3. Sort by similarity score
    // 4. Return top N results
}

// Hybrid search combines keyword (70%) + semantic (30%)
func HybridSearch(query string, limit int) []*SearchResult {
    keywordResults := BleveSearch(query, limit*2)
    semanticResults := SemanticSearch(Embed(query), limit*2)
    return MergeWithWeights(keywordResults, semanticResults, 0.7, 0.3)
}
```

### 3.4 Component Design

#### Sync Worker
- **Responsibilities**: Discover posts, fetch content, detect changes, update storage
- **Sync Strategy**:
  - **Discovery**: Use GraphQL search with pagination to find all post IDs
  - **Metadata**: Batch fetch post metadata (title, dates, author) via GraphQL
  - **Content**: Concurrently fetch markdown via `/posts/{id}/export/markdown`
  - **Change Detection**: Compare MD5 hash of content
  - **Optimization**: Only fetch markdown for posts with changed `updatedAt`
- **Performance**:
  - Parallel markdown fetching (10-20 concurrent workers)
  - Expected sync time: ~2 seconds per 100 posts
- **Error Handling**: Exponential backoff, partial sync recovery, continue on individual failures
- **Monitoring**: Log sync duration, document count, errors, API rate limits

#### Search Service
- **Index Management**: Build, update, and optimize Bleve index
- **Query Processing**: Parse user input, handle operators, apply filters
- **Ranking**: Title matches weighted higher than content
- **Highlighting**: Return snippets with matched terms highlighted

#### Web Interface
- **Endpoints**:
  - `GET /` - Search page
  - `GET /api/search?q=...` - Search API
  - `GET /api/stats` - System statistics
  - `GET /health` - Health check endpoint
- **UI Components**:
  - Search bar with real-time suggestions
  - Result cards with previews
  - Filter sidebar (author, date, topics)
  - Pagination or infinite scroll

### 3.5 Configuration

```yaml
# config.yaml example
slab:
  jwt_token: ${SLAB_JWT_TOKEN}              # JWT for authentication
  graphql_url: "https://slab.render.com/graphql"
  base_url: "https://slab.render.com"

storage:
  data_dir: "./data"
  sqlite_db: "./data/slab.db"
  index_dir: "./data/bleve"

sync:
  schedule: "0 2 * * *"        # 2 AM daily
  concurrency: 10              # Parallel markdown fetches
  timeout: 1800s
  include_archived: false      # Skip archived posts

server:
  port: 8080
  host: "localhost"

search:
  max_results: 50
  snippet_length: 200
  highlight_tag: "<mark>"

embeddings:
  enabled: true                # Enable semantic search
  provider: "ollama"           # "ollama" (local) or "openai" (cloud)
  ollama_url: "http://localhost:11434"
  model: "nomic-embed-text"    # Ollama model name (768-dim)
  hybrid_weight: 0.3           # Semantic weight in hybrid search (0.0-1.0)
```

### 3.6 Deployment Options

#### Local Deployment
```bash
# Single binary
./slab-search server --config=./config.yaml

# Or using systemd service
systemctl start slab-search
```

#### Hosted Deployment (Render)
```yaml
# render.yaml
services:
  - type: web
    name: slab-search
    env: go
    buildCommand: go build -o app ./cmd/server
    startCommand: ./app
    envVars:
      - key: SLAB_API_TOKEN
        sync: false
    disk:
      name: data
      mountPath: /data
      sizeGB: 10

  - type: cron
    name: slab-search-sync
    env: go
    buildCommand: go build -o sync ./cmd/sync
    startCommand: ./sync
    schedule: "@daily"
```

## 4. Implementation Phases

### Phase 1: MVP (Week 1-2)
- [ ] Slab API client (GraphQL + HTTP markdown export)
- [ ] Topic iteration for post discovery
- [ ] SQLite storage with documents table
- [ ] Bleve index with fuzzy matching
- [ ] Markdown content indexing
- [ ] Manual sync command: `slab-search sync`
- [ ] CLI search: `slab-search search "query"`
- [ ] Simple web UI with search (Go templates + HTMX)
- [ ] Content hash-based change detection
- [ ] Basic logging and error handling

### Phase 2: Enhancement (Week 3-4)
- [ ] Semantic search with Ollama embeddings (nomic-embed-text, 768-dim)
  - Setup Ollama service (systemd)
  - Add `embedding BLOB` column to documents table
  - Generate embeddings during sync (~8min for 10k docs)
  - Implement cosine similarity search (brute-force, ~40ms)
- [ ] Hybrid scoring (keyword 70% + semantic 30%)
- [ ] Author and date filtering
- [ ] Improved UI with HTMX interactivity
- [ ] Automated daily sync (cron/scheduler)
- [ ] Search result highlighting improvements
- [ ] Performance optimizations
- [ ] Incremental sync (only changed docs) ✅ DONE (timestamp-based)
- [ ] Monitoring dashboard

**See `EMBEDDINGS_IMPLEMENTATION.md` for detailed semantic search design and implementation plan.**

### Phase 3: Production (Week 5-6)
- [ ] Incremental sync using changes API
- [ ] Deployment to Render
- [ ] Authentication (if needed)
- [ ] Search analytics dashboard
- [ ] Admin interface
- [ ] Load testing

## 5. Testing Strategy

### Unit Tests
- Slab client GraphQL queries
- Search index operations
- Document storage and retrieval
- Query parsing and filtering

### Integration Tests
- Full sync workflow
- Search accuracy with test corpus
- Web UI interaction flows
- Error recovery scenarios

### Performance Tests
- Search latency with 10,000 documents
- Sync duration and resource usage
- Concurrent user load
- Index size and memory footprint

## 6. Monitoring and Metrics

### Key Metrics
- Search latency (p50, p95, p99)
- Sync success rate
- Documents indexed
- Query volume and patterns
- Error rates by component

### Alerting Thresholds
- Sync failure > 2 consecutive days
- Search latency p95 > 2 seconds
- Disk usage > 80%
- Memory usage > 2GB

## 7. Risks and Mitigations

| Risk | Impact | Probability | Mitigation |
|------|--------|-------------|------------|
| Slab API changes | High | Low | Version lock API client, monitor deprecations |
| Data volume growth | Medium | Medium | Implement data retention, optimize compression |
| Search quality issues | High | Medium | A/B test with users, collect feedback |
| Sync performance degradation | Medium | Medium | Implement incremental sync, parallel processing |
| Security breach via search | High | Low | Respect Slab permissions, sanitize inputs |

## 8. Open Questions

1. Should we support search within specific Slab collections/folders?
2. Do we need to handle document versioning/history?
3. Should search queries be shareable via URL parameters?
4. Is there a need for "advanced search" with complex boolean logic?
5. Should we integrate with other tools (Slack notifications, browser extensions)?

## 9. Success Metrics

### Quantitative
- 90% reduction in time to find documents
- < 1 second average search response time
- 100% of public documents indexed successfully
- < 30 minute daily sync time

### Qualitative
- Positive user feedback on search relevance
- Increased usage of historical documentation
- Reduced duplicate document creation
- Better knowledge sharing across teams

## Appendix A: Slab GraphQL Queries (Actual)

### Search Posts (Discovery)
```graphql
query SearchPosts($query: String!, $first: Int, $after: String) {
  search(query: $query, first: $first, after: $after) {
    edges {
      node {
        ... on PostSearchResult {
          post {
            id
            title
            publishedAt
            updatedAt
            archivedAt
          }
        }
      }
      cursor
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}
```

### Get Single Post (Metadata)
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

### Fetch Markdown Content (HTTP)
```
GET https://slab.render.com/posts/{id}/export/markdown
Authorization: Bearer {jwt_token}

Returns: text/markdown
```

### Key Types
```typescript
type Post {
  id: ID!
  title: String!
  publishedAt: Datetime
  updatedAt: Datetime
  archivedAt: Datetime
  owner: User
  topics: [Topic!]!
}

type PostSearchResult {
  title: String          // HTML with <em> highlights
  highlight: String      // JSON showing match positions
  post: Post!
}

type SearchResultConnection {
  edges: [SearchResultEdge!]!
  pageInfo: PageInfo!
}
```

## Appendix B: Example Search Queries

- `"deployment process"` - Exact phrase
- `kubernetes deploy` - Multiple terms
- `author:john monitoring` - Author filter
- `updated:2024-01-01..2024-12-31 performance` - Date range
- `topic:engineering latency` - Topic filter