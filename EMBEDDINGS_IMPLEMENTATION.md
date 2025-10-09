# Semantic Search Implementation - Embeddings Architecture

**Decision Date**: 2025-10-09
**Status**: Approved - Ready for Implementation
**Chosen Approach**: Ollama with nomic-embed-text

## Context

To reach MVP per `design.md:62-65`, we need to implement semantic search with embeddings. This document captures the evaluation of all local embedding generation options and the rationale for choosing Ollama.

## Requirements

From `design.md`:

- **Semantic search**: Find conceptually related content using embeddings (line 63)
- **Hybrid scoring**: Combine keyword and semantic search (line 64)
- **Local deployment**: No external API dependencies (line 326)
- **Performance**: Fast enough for 10,023 documents
- **Phase 2 feature**: After MVP keyword search is working (line 381-389)

## Options Evaluated

### Summary Comparison

| **Option** | **Setup** | **Go Integration** | **Performance** | **Dependencies** | **Production Ready?** | **Implementation Effort** |
|------------|-----------|-------------------|-----------------|------------------|---------------------|--------------------------|
| **1. Ollama** | ⭐⭐⭐ Easiest | ⭐⭐⭐ HTTP API | 15-50ms/embed | Ollama daemon | ✅ Yes | 2-3 hours |
| **2. fastembed-go** | ⭐⭐ Medium | ⭐⭐⭐ Native | 30-100ms | CGo + ONNX libs | ⚠️ Maybe | 4-6 hours |
| **3. chromem-go** | ⭐⭐⭐ Pure Go | ⭐⭐⭐ Native | N/A* | None (but needs Ollama for embeds) | ⚠️ Maybe | 3-5 hours |
| **4. onnxruntime-go** | ⭐ Hardest | ⭐ Low-level | 30-100ms | CGo + ONNX libs | ❌ Risky | 1-2 days |
| **5. Python subprocess** | ⭐⭐ Medium | ⭐ Subprocess | 50-150ms | Python + packages | ✅ Yes | 3-4 hours |

*chromem-go is a vector database, not an embedding generator. It delegates to Ollama/OpenAI for embeddings.

---

## Option 1: Ollama (CHOSEN)

### Overview

Ollama is a local LLM server with a simple HTTP API for generating embeddings. It manages models and provides GPU acceleration when available.

### Setup

```bash
# Install Ollama
curl -fsSL https://ollama.com/install.sh | sh

# Download embedding model (274MB)
ollama pull nomic-embed-text

# Run as systemd service
systemctl start ollama
systemctl enable ollama
```

### Go Implementation

```go
import (
    "bytes"
    "encoding/json"
    "net/http"
)

type OllamaClient struct {
    baseURL string // http://localhost:11434
}

type embedRequest struct {
    Model string   `json:"model"`
    Input []string `json:"input"`
}

type embedResponse struct {
    Embeddings [][]float32 `json:"embeddings"`
}

func (c *OllamaClient) Embed(text string) ([]float32, error) {
    req := embedRequest{
        Model: "nomic-embed-text",
        Input: []string{text},
    }

    body, _ := json.Marshal(req)
    resp, err := http.Post(c.baseURL+"/api/embed", "application/json", bytes.NewReader(body))
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    var embedResp embedResponse
    json.NewDecoder(resp.Body).Decode(&embedResp)
    return embedResp.Embeddings[0], nil
}
```

### Pros

- ✅ **Production-ready**: Microsoft-backed, actively maintained, used by ChromaDB, Docker, etc.
- ✅ **Simple API**: Clean HTTP interface, ~50 lines of Go code
- ✅ **Easy deployment**: Single systemd service, no build complications
- ✅ **Model flexibility**: Swap models with `ollama pull <model>` (no code changes)
- ✅ **Performance**: 15-50ms per embedding on CPU, GPU-accelerated if available
- ✅ **Future-proof**: Can add chat, reasoning, tool use features later
- ✅ **Official Go SDK**: `github.com/ollama/ollama/api` available
- ✅ **Battle-tested**: Large community, stable API

### Cons

- ⚠️ **Daemon dependency**: Requires Ollama service running (managed by systemd)
- ⚠️ **Not truly embedded**: External process, but transparent to users
- ⚠️ **Model size**: 274MB for nomic-embed-text (one-time download)

### Performance Analysis

For our dataset (10,023 documents):

**Initial sync** (generate all embeddings):
- Time: 50ms × 10,023 = ~8 minutes
- Network: localhost (no network latency)
- One-time cost

**Incremental sync** (changed documents):
- Typical: 10-50 docs/day
- Time: 50ms × 50 = 2.5 seconds
- Negligible impact

**Search time** (hybrid keyword + semantic):
- Bleve keyword search: <10ms
- Cosine similarity (10k vectors): 30-40ms (brute-force is fine)
- Total: <50ms

### Why Ollama Over ONNX?

Direct ONNX Runtime integration was considered but rejected because:

1. **Maturity**: onnxruntime-go is community-maintained, not official
   - No Microsoft support for Go bindings
   - Breaking changes risk if binding lags official API
   - Used by only 60 packages vs Ollama's massive adoption

2. **Complexity**: ONNX requires:
   - Manual tokenization (BERT tokenizer in Go)
   - Tensor creation and management
   - Model file management
   - 500+ lines of boilerplate vs 50 lines for Ollama

3. **CGo**: Both require native libraries
   - onnxruntime-go: 150MB of ONNX Runtime libs + CGo
   - Ollama: Separate daemon (cleaner separation)

4. **Deployment**:
   - ONNX: Cross-compilation pain with CGo
   - Ollama: Single systemd service

5. **Flexibility**:
   - ONNX: Changing models requires code changes
   - Ollama: `ollama pull <new-model>` (no recompile)

6. **Performance**: Negligible difference at our scale
   - ONNX: 30-50ms per embedding
   - Ollama: 15-50ms per embedding
   - Difference: <1 minute for full re-sync

**Verdict**: The 2-3 day implementation effort and ongoing maintenance burden of ONNX is not justified for a 1-2 minute performance difference on initial sync.

---

## Option 2: fastembed-go

### Overview

Go library wrapping ONNX Runtime with pre-configured embedding models (BGE, MiniLM).

### Implementation

```go
import "github.com/anush008/fastembed-go"

embedder, _ := fastembed.NewFlagEmbedding(fastembed.DefaultModel, 256)
vectors, _ := embedder.Embed([]string{"text here"}, 1)
```

### Pros

- ✅ Purpose-built for embeddings
- ✅ Parallel batch processing with goroutines
- ✅ Pre-configured popular models

### Cons

- ❌ Uses unofficial onnxruntime-go binding underneath
- ❌ CGo required (cross-compilation complexity)
- ❌ Must ship 150MB of ONNX Runtime libraries
- ❌ Community-maintained (less mature than Ollama)
- ❌ Newer project, fewer production deployments

### Decision

**Rejected**: Adds CGo complexity without significant benefit over Ollama. Could be reconsidered if we need true in-process embeddings later.

---

## Option 3: chromem-go

### Overview

Pure Go vector database with zero dependencies. Great for vector search, but delegates embedding generation to Ollama or OpenAI.

### Implementation

```go
import "github.com/philippgille/chromem-go"

db := chromem.NewDB()
collection, _ := db.CreateCollection("docs", nil, chromem.NewEmbeddingFuncOllama(...))
collection.AddDocuments(...)
results, _ := collection.Query("search query", 5, nil, nil)
```

### Pros

- ✅ Zero dependencies (pure Go)
- ✅ Fast vector search: 40ms for 100k documents
- ✅ In-memory or disk-backed
- ✅ No CGo

### Cons

- ❌ Doesn't generate embeddings itself (still needs Ollama/OpenAI)
- ❌ Adds abstraction layer we don't need yet
- ❌ Newer project (less battle-tested)

### Decision

**Deferred**: Interesting for future optimization. At 10k documents, brute-force cosine similarity is fast enough (<40ms). Could add chromem-go later if we scale to 100k+ documents.

---

## Option 4: onnxruntime-go (Direct)

### Overview

Low-level Go bindings for ONNX Runtime. Maximum control but maximum complexity.

### Implementation

Would require:
- Loading ONNX model file (model.onnx)
- Implementing BERT tokenizer in Go
- Manual tensor creation
- Managing input/output tensors
- ~500+ lines of code

### Pros

- ✅ Maximum control
- ✅ No daemon
- ✅ ONNX Runtime is fast (3x with quantization)

### Cons

- ❌ **Unofficial Go binding**: No Microsoft support
- ❌ **Huge implementation effort**: 1-2 days minimum
- ❌ **CGo complexity**: Cross-compilation issues
- ❌ **Tokenizer required**: BERT tokenizer is complex
- ❌ **Maintenance risk**: Binding may lag official API
- ❌ **Still ships libraries**: 150MB of ONNX Runtime libs

### Decision

**Rejected**: 1-2 day implementation effort + ongoing maintenance burden not justified for negligible performance gain at our scale.

---

## Option 5: Python Subprocess

### Overview

Pragmatic hybrid: Generate embeddings via Python subprocess, return JSON to Go.

### Implementation

```go
func generateEmbedding(text string) ([]float32, error) {
    script := `
import sys
from sentence_transformers import SentenceTransformer
model = SentenceTransformer('all-MiniLM-L6-v2')
text = sys.stdin.read()
embedding = model.encode([text])[0].tolist()
print(json.dumps(embedding))
`
    cmd := exec.Command("python3", "-c", script)
    cmd.Stdin = strings.NewReader(text)
    output, _ := cmd.Output()
    // Parse JSON
}
```

### Pros

- ✅ Easiest embedding generation (sentence-transformers "just works")
- ✅ Access to entire HuggingFace ecosystem
- ✅ Battle-tested Python ML stack
- ✅ Simple Go code (subprocess + JSON)

### Cons

- ❌ Subprocess overhead: 50-150ms per call
- ❌ Python dependency (must ship Python + packages)
- ❌ Process startup cost (can batch to amortize)
- ❌ Not elegant

### Decision

**Rejected**: Works, but Ollama provides better performance and cleaner architecture. Valid fallback option if Ollama proves problematic.

---

## Implementation Plan with Ollama

### Phase 1: Database Schema (30 min)

```sql
-- Add embedding column to documents table
ALTER TABLE documents ADD COLUMN embedding BLOB;

-- Store as binary BLOB: 384 floats × 4 bytes = 1,536 bytes per document
-- Total: 1.5KB × 10,023 = ~15MB for all embeddings
```

### Phase 2: Embedding Service (1 hour)

```go
// internal/embeddings/ollama.go

type Client struct {
    baseURL string
    model   string
    client  *http.Client
}

func NewClient(baseURL, model string) *Client {
    return &Client{
        baseURL: baseURL,
        model:   model,
        client:  &http.Client{Timeout: 30 * time.Second},
    }
}

func (c *Client) Embed(text string) ([]float32, error) {
    // POST to /api/embed
    // Return []float32 vector
}

func (c *Client) EmbedBatch(texts []string) ([][]float32, error) {
    // Batch embedding for efficiency
}

// Helper: serialize/deserialize embeddings to/from BLOB
func SerializeEmbedding(vec []float32) []byte {
    // Convert to binary for SQLite storage
}

func DeserializeEmbedding(data []byte) []float32 {
    // Convert from binary
}
```

### Phase 3: Update Sync Worker (1 hour)

```go
// internal/sync/worker.go

// Update to generate embeddings during sync
func (w *Worker) syncDocument(post Post) error {
    // 1. Fetch markdown (existing)
    content := fetchMarkdown(post.ID)

    // 2. Generate embedding (NEW)
    embedding, err := w.embedder.Embed(content)
    if err != nil {
        log.Warn("Failed to generate embedding", "id", post.ID, "error", err)
        // Continue without embedding (graceful degradation)
    }

    // 3. Store document + embedding
    doc := &storage.Document{
        // ... existing fields
        Embedding: embeddings.SerializeEmbedding(embedding),
    }
    w.db.Upsert(doc)
}
```

### Phase 4: Semantic Search (1.5 hours)

```go
// internal/search/semantic.go

// CosineSimilarity computes similarity between two vectors
func CosineSimilarity(a, b []float32) float32 {
    var dotProduct, normA, normB float32
    for i := range a {
        dotProduct += a[i] * b[i]
        normA += a[i] * a[i]
        normB += b[i] * b[i]
    }
    return dotProduct / (sqrt(normA) * sqrt(normB))
}

// SemanticSearch finds similar documents by embedding
func (i *Index) SemanticSearch(queryEmbedding []float32, limit int) ([]*SearchResult, error) {
    // 1. Get all documents from DB (with embeddings)
    docs, err := i.db.List(false)

    // 2. Compute cosine similarity for each
    type scoredDoc struct {
        doc   *storage.Document
        score float32
    }

    scores := make([]scoredDoc, 0, len(docs))
    for _, doc := range docs {
        if len(doc.Embedding) == 0 {
            continue // Skip docs without embeddings
        }

        embedding := embeddings.DeserializeEmbedding(doc.Embedding)
        score := CosineSimilarity(queryEmbedding, embedding)
        scores = append(scores, scoredDoc{doc, score})
    }

    // 3. Sort by score (descending)
    sort.Slice(scores, func(i, j int) bool {
        return scores[i].score > scores[j].score
    })

    // 4. Return top N
    results := make([]*SearchResult, 0, limit)
    for i := 0; i < min(limit, len(scores)); i++ {
        results = append(results, &SearchResult{
            ID:      scores[i].doc.ID,
            Title:   scores[i].doc.Title,
            Author:  scores[i].doc.AuthorName,
            SlabURL: scores[i].doc.SlabURL,
            Score:   float64(scores[i].score),
        })
    }

    return results, nil
}
```

### Phase 5: Hybrid Search (30 min)

```go
// internal/search/index.go

func (i *Index) HybridSearch(query string, limit int, embedder *embeddings.Client) ([]*SearchResult, error) {
    // 1. Keyword search with Bleve
    keywordResults, _ := i.Search(query, limit*2) // Get more candidates

    // 2. Semantic search
    queryEmbedding, _ := embedder.Embed(query)
    semanticResults, _ := i.SemanticSearch(queryEmbedding, limit*2)

    // 3. Merge results with weighted scoring
    // Keyword: 70%, Semantic: 30% (can tune)
    merged := mergeResults(keywordResults, semanticResults, 0.7, 0.3)

    // 4. Return top N
    return merged[:limit], nil
}

func mergeResults(keyword, semantic []*SearchResult, keywordWeight, semanticWeight float64) []*SearchResult {
    // Normalize scores to 0-1 range
    keywordScores := normalizeScores(keyword)
    semanticScores := normalizeScores(semantic)

    // Combine by ID
    scoreMap := make(map[string]*SearchResult)
    for id, keywordScore := range keywordScores {
        semanticScore := semanticScores[id] // 0 if not found
        combinedScore := keywordWeight*keywordScore + semanticWeight*semanticScore

        // Create merged result (use keyword result as base)
        result := *keywordResults[id] // Copy
        result.Score = combinedScore
        scoreMap[id] = &result
    }

    // Sort by combined score
    results := make([]*SearchResult, 0, len(scoreMap))
    for _, result := range scoreMap {
        results = append(results, result)
    }
    sort.Slice(results, func(i, j int) bool {
        return results[i].Score > results[j].Score
    })

    return results
}
```

### Phase 6: CLI Updates (30 min)

```go
// cmd/slab-search/main.go

// Add flags
var (
    useSemanticSearch bool
    hybridWeight      float64 // 0.0 = keyword only, 1.0 = semantic only
)

func init() {
    searchCmd.Flags().BoolVar(&useSemanticSearch, "semantic", false, "Use semantic search")
    searchCmd.Flags().Float64Var(&hybridWeight, "hybrid", 0.3, "Semantic weight (0.0-1.0)")
}

// Usage:
// ./slab-search search "kubernetes" --semantic
// ./slab-search search "kubernetes" --hybrid=0.5
```

### Phase 7: Deployment (15 min)

```bash
# Install Ollama on server
curl -fsSL https://ollama.com/install.sh | sh

# Download model
ollama pull nomic-embed-text

# Enable systemd service
systemctl enable ollama
systemctl start ollama

# Verify
curl http://localhost:11434/api/embed -d '{
  "model": "nomic-embed-text",
  "input": ["test"]
}'
```

---

## Performance Expectations

### Embedding Generation

- **CPU (typical)**: 50ms per document
- **GPU (if available)**: 15-30ms per document
- **Batch optimization**: Can batch 10-20 at a time for better throughput

### Initial Sync

- **Full dataset**: 10,023 documents
- **Time**: 50ms × 10,023 = ~8 minutes
- **Storage**: 1.5KB × 10,023 = ~15MB additional DB size

### Incremental Sync

- **Daily changes**: ~10-50 documents
- **Time**: 50ms × 50 = 2.5 seconds
- **Impact**: Negligible

### Search Performance

- **Keyword (Bleve)**: <10ms
- **Semantic (brute-force)**: 30-40ms for 10k vectors
- **Hybrid**: <50ms total
- **Goal**: <100ms p95 (well within target)

---

## Future Optimizations

If we scale beyond 10k documents or need faster search:

### Option A: chromem-go for Vector Search

Replace brute-force cosine similarity with chromem-go's optimized HNSW index:
- Current: 40ms for 10k documents
- chromem-go: <5ms for 100k documents

### Option B: sqlite-vss

Use SQLite extension for vector similarity search:
- Keeps vectors in SQLite (no separate database)
- HNSW indexing for fast search
- Requires SQLite extension

### Option C: Quantization

Reduce embedding dimensions (384 → 256 or 128):
- Faster similarity computation
- Lower storage (1.5KB → 512 bytes)
- Minimal accuracy loss

### Option D: Switch to fastembed-go

If Ollama daemon becomes problematic:
- True in-process embedding generation
- Similar performance
- Adds CGo complexity

---

## Testing Plan

### Unit Tests

```go
func TestOllamaClient_Embed(t *testing.T) {
    // Test embedding generation
    client := embeddings.NewClient("http://localhost:11434", "nomic-embed-text")

    embedding, err := client.Embed("test document")
    assert.NoError(t, err)
    assert.Len(t, embedding, 384) // nomic-embed-text dimension
}

func TestCosineSimilarity(t *testing.T) {
    // Test similarity computation
    a := []float32{1, 0, 0}
    b := []float32{1, 0, 0}
    assert.Equal(t, 1.0, CosineSimilarity(a, b)) // Identical

    c := []float32{0, 1, 0}
    assert.Equal(t, 0.0, CosineSimilarity(a, c)) // Orthogonal
}
```

### Integration Tests

```go
func TestSemanticSearch(t *testing.T) {
    // Setup test DB with sample documents
    docs := []*storage.Document{
        {ID: "1", Content: "Kubernetes deployment guide", ...},
        {ID: "2", Content: "Python programming tutorial", ...},
        {ID: "3", Content: "Docker container orchestration", ...},
    }

    // Generate embeddings
    for _, doc := range docs {
        embedding, _ := embedder.Embed(doc.Content)
        doc.Embedding = embeddings.SerializeEmbedding(embedding)
    }

    // Test semantic search
    results, _ := index.SemanticSearch(embedder.Embed("kubernetes"), 10)

    // Should return doc 1 and 3 (related to k8s/containers)
    assert.Contains(t, results[0].ID, []string{"1", "3"})
}
```

### Manual Testing

```bash
# Test embedding generation
curl http://localhost:11434/api/embed -d '{
  "model": "nomic-embed-text",
  "input": ["kubernetes deployment"]
}'

# Test semantic search
./slab-search search "database optimization" --semantic

# Test hybrid search
./slab-search search "database optimization" --hybrid=0.3

# Compare results
./slab-search search "database optimization"  # Keyword only
./slab-search search "database optimization" --semantic  # Semantic only
./slab-search search "database optimization" --hybrid=0.5  # Balanced
```

---

## Rollout Plan

### Phase 1: Add Embeddings (Non-breaking)

1. Add `embedding BLOB` column to database
2. Update sync to generate embeddings (optional)
3. Documents without embeddings still work (graceful degradation)

### Phase 2: Enable Semantic Search (Opt-in)

1. Add `--semantic` flag to search command
2. Only use if embeddings exist
3. Falls back to keyword search if no embeddings

### Phase 3: Enable Hybrid by Default (Default)

1. Make hybrid search the default
2. Automatically detect if embeddings available
3. Fallback to keyword-only if Ollama unavailable

### Phase 4: Backfill Embeddings

```bash
# Generate embeddings for all existing documents
./slab-search generate-embeddings

# Shows progress:
# Generating embeddings: 5000/10023 (49.9%)
# ETA: 4m 30s
```

---

## Monitoring and Metrics

### Key Metrics

- **Embedding generation rate**: embeddings/second
- **Embedding failures**: count and error types
- **Search latency**: keyword vs semantic vs hybrid
- **Result quality**: click-through rate by search type

### Health Checks

```bash
# Verify Ollama is running
curl http://localhost:11434/api/tags

# Check model availability
curl http://localhost:11434/api/show -d '{"name":"nomic-embed-text"}'

# Test embedding generation
curl http://localhost:11434/api/embed -d '{
  "model": "nomic-embed-text",
  "input": ["health check"]
}'
```

---

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Ollama service down | Semantic search unavailable | Graceful fallback to keyword search |
| Embedding generation slow | Sync takes longer | Batch embeddings, optimize later |
| Model size too large | Deployment complexity | 274MB is acceptable; can use smaller models |
| Search quality issues | Poor user experience | Tune hybrid weights, collect feedback |
| Storage growth | Database size | 15MB for 10k docs is fine; monitor growth |

---

## Success Criteria

- ✅ Semantic search returns relevant results for conceptual queries
- ✅ Hybrid search improves relevance over keyword-only
- ✅ Search latency stays under 100ms p95
- ✅ Initial sync completes in under 10 minutes
- ✅ Incremental sync impact is negligible (<5 seconds)
- ✅ System degrades gracefully if Ollama unavailable

---

## References

- [Ollama Documentation](https://ollama.com/docs)
- [nomic-embed-text Model](https://ollama.com/library/nomic-embed-text)
- [Ollama API Reference](https://github.com/ollama/ollama/blob/main/docs/api.md)
- [design.md](./design.md) - Original design document
- [SEARCH_IMPROVEMENTS.md](./SEARCH_IMPROVEMENTS.md) - Keyword search improvements

---

## Appendix: Alternative Models

If nomic-embed-text doesn't work well, alternatives:

| Model | Dimension | Size | Performance | Use Case |
|-------|-----------|------|-------------|----------|
| nomic-embed-text | 768 | 274MB | Best quality | Default choice |
| all-minilm | 384 | 120MB | Faster, smaller | Resource-constrained |
| bge-large | 1024 | 1.3GB | Highest quality | Maximum accuracy |
| mxbai-embed-large | 1024 | 670MB | Good balance | Alternative to BGE |

Switch models with: `ollama pull <model-name>`
