# Slab GraphQL API - Key Findings

Based on schema exploration and API testing with `slab.render.com`

## Authentication
- **Method**: JWT token in `Authorization: Bearer <token>` header
- **Endpoint**: `https://slab.render.com/graphql`
- **Introspection**: Disabled (security best practice for production)

## Core Queries

### 1. Search Posts
```graphql
query SearchPosts($query: String!, $first: Int, $after: String) {
  search(query: $query, first: $first, after: $after) {
    edges {
      node {
        ... on PostSearchResult {
          title              # HTML with <em> tags for highlights
          highlight          # JSON format showing match positions
          post {
            id
            title           # Plain text
            publishedAt
            updatedAt
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

**Notes:**
- Returns `SearchResultConnection` (cursor-based pagination)
- Results can be: `PostSearchResult`, `CommentSearchResult`, `TopicSearchResult`, `UserSearchResult`, `GroupSearchResult`
- Use fragment `... on PostSearchResult` to filter post results
- `highlight` field contains JSON showing where matches occurred

### 2. Get Single Post
```graphql
query GetPost($id: ID!) {
  post(id: $id) {
    id
    title
    content          # JSON format (Quill Delta)
    publishedAt
    updatedAt
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

**Notes:**
- `content` is JSON (not markdown!) using Quill Delta format
- Need to convert JSON to plain text/markdown for indexing

### 3. List All Post IDs (NOT WORKING YET)
The schema shows `organization.posts` but it requires specific access or arguments.
Need to explore alternatives:
- Use `search(query: "*")` or empty query?
- Iterate through topics?
- Use export API?

## Data Types

### Post Type
```typescript
type Post {
  id: ID!
  title: String!
  content: Json!         // Quill Delta format
  publishedAt: Datetime
  updatedAt: Datetime
  insertedAt: Datetime
  archivedAt: Datetime
  owner: User
  topics: [Topic!]!
  linkAccess: PostLinkAccess
  version: Int
  banner: Image
}
```

### SlimPost Type (Metadata Only)
```typescript
type SlimPost {
  id: ID!
  title: String!
  publishedAt: Datetime
  archivedAt: Datetime
  linkAccess: PostLinkAccess
  topics: [Topic!]!
}
```

### Content Format (Quill Delta)
Posts use [Quill Delta](https://quilljs.com/docs/delta/) JSON format:

```json
[
  { "insert": "Gandalf", "attributes": { "bold": true } },
  { "insert": " the " },
  { "insert": "Grey", "attributes": { "italic": true } }
]
```

**Conversion needed**: We must convert this to plain text or markdown for search indexing.

## Rate Limiting
- API includes rate limit info in response extensions:
```json
"rateLimit": {
  "used": 2939,
  "remaining": 49997061,
  "cost": 4
}
```

## Performance
- Query complexity tracking in extensions
- Search took ~187ms for 5 results
- Single post fetch took ~2ms
- Very fast API!

## Key Implementation Details Needed

### 1. Content Conversion ✅ **CRITICAL**
- **Problem**: Content is in Quill Delta JSON, not markdown
- **Solution Options**:
  a. Use a Quill Delta → Markdown/Text converter library
  b. Extract plain text from Delta format ourselves
  c. Store both formats (JSON original + extracted text)

### 2. Bulk Post Fetching ⚠️ **NEEDS INVESTIGATION**
- **Problem**: `posts(ids: [ID!]!)` requires knowing all IDs upfront
- **Current Solution**: Use search to discover posts, then fetch individually
- **Better Solutions to Explore**:
  - Can we use `search(query: "")` with empty query to get all?
  - Is there a topics → posts relationship we can traverse?
  - Should we use the export API instead?

### 3. Incremental Sync Strategy
- **Approach**: Query posts by `updatedAt` range?
- **Need to verify**: Can we filter/sort by dates in search?
- **Fallback**: Store last sync timestamp, fetch all, compare hashes

### 4. Content Checksum
- API doesn't provide content hash
- We need to compute our own (MD5/SHA256 of content JSON)
- Store in our DB for change detection

### 5. URL Generation
- Post URLs follow pattern: `https://slab.render.com/posts/{slug}-{id}`
- Slug is derived from title
- Need to either:
  a. Generate slug from title ourselves
  b. Fetch URL from a field we haven't found yet
  c. Use generic format: `https://slab.render.com/posts/{id}`

## Updated Architecture Decisions

### Content Storage
- **Original**: Store markdown in SQLite
- **Actual**: Store Quill Delta JSON + extracted plain text
```sql
CREATE TABLE documents (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    content_json TEXT NOT NULL,      -- Original Quill Delta
    content_text TEXT NOT NULL,       -- Extracted plain text
    content_hash TEXT NOT NULL,       -- For change detection
    ...
);
```

### Search Index (Bleve)
- Index the extracted plain text, not JSON
- Title gets higher boost
- Can still return snippets with highlighting

### Sync Strategy (MVP)
1. Use `search(query: "")` to discover all post IDs (if empty query works)
2. Fetch posts in batches using `post(id: $id)`
3. Convert Quill Delta → plain text
4. Compute hash for change detection
5. Store in SQLite + update Bleve index

## BREAKTHROUGH: Markdown Export Endpoint! 🎉

**Discovery**: Posts have a direct markdown export endpoint:
```
GET https://slab.render.com/posts/{post-id}/export/markdown
Authorization: Bearer {jwt}
```

This returns clean, ready-to-index markdown - **NO QUILL PARSING NEEDED!**

## CRITICAL: Use currentSession for API Access

**Discovery during implementation**: JWT tokens provide viewer access, which returns `PublicOrganization` type that lacks `topics` field.

**Solution**: Use `currentSession` query to get full `Organization` type:

```graphql
{
  currentSession {
    organization {
      topics { id name }
      posts { id title ... }
    }
  }
}
```

This provides access to all topics and posts without requiring organization host parameter.

### Final Implementation Strategy (VALIDATED ✅)

**Step 1: Get all topics** (GraphQL)
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

**Step 2: Get posts per topic** (GraphQL with connection pattern)
```graphql
query GetTopicPosts($topicId: ID!, $first: Int) {
  topic(id: $topicId) {
    posts(first: $first) {  # REQUIRED: must specify first or last
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

**Step 3: Fetch markdown** (HTTP)
```
GET /posts/{id}/export/markdown → Clean markdown
```

**Performance (Measured)**:
- 10 posts synced in 1.03 seconds
- Topic discovery: ~1 second (1081 topics)
- Markdown fetch: ~100ms per post with 5 concurrent workers
- Change detection via MD5 hash (skip unchanged)
- Expected: ~10-20 seconds for 100 posts

### Database Schema (Updated)

```sql
CREATE TABLE documents (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    content TEXT NOT NULL,           -- Markdown from export endpoint
    content_hash TEXT NOT NULL,      -- MD5 of content for change detection
    author_name TEXT,
    author_email TEXT,
    slab_url TEXT NOT NULL,          -- https://slab.render.com/posts/{id}
    topics TEXT,                     -- JSON array
    published_at TIMESTAMP,
    updated_at TIMESTAMP,
    embedding TEXT                   -- JSON array for semantic search
);
```

### Sync Algorithm (Final)

```go
// 1. Discover posts via search (or iterate topics)
posts := searchAllPosts() // Use pagination

// 2. Fetch metadata via GraphQL (batch)
metadata := batchFetchMetadata(posts)

// 3. Fetch markdown concurrently
markdownChan := make(chan PostWithMarkdown, 100)
for _, post := range metadata {
    go func(p Post) {
        md := fetchMarkdown(p.ID)
        markdownChan <- PostWithMarkdown{Post: p, Markdown: md}
    }(post)
}

// 4. Index
for pwm := range markdownChan {
    hash := md5(pwm.Markdown)
    if existingHash != hash {
        db.Upsert(pwm)
        bleveIndex.Index(pwm)
    }
}
```

## Questions Answered ✅

1. ~~**Bulk fetching**~~ → Use search with pagination
2. ~~**Content format**~~ → Markdown export endpoint (no Quill parsing!)
3. ~~**URL generation**~~ → Simple: `https://slab.render.com/posts/{id}`
4. **Embeddings** → Embed the markdown text (works great!)
5. **Archived posts** → Filter out where `archivedAt != null` (optional)

## Next Steps

1. ✅ API exploration complete
2. ✅ Markdown export endpoint discovered
3. ✅ Combined strategy validated
4. 🔲 Update design.md with corrected approach
5. 🔲 Begin MVP implementation
