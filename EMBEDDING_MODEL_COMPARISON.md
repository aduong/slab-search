# Embedding Model Comparison & Dual Model Support

**Date**: 2025-10-13
**Status**: Implemented - Dual embedding support (nomic + qwen)

## Overview

We implemented support for multiple embedding models to enable A/B testing of search quality. The system now supports storing embeddings from two different models side-by-side:
- **nomic-embed-text** (default) - 768 dimensions
- **qwen3-embedding** - 4096 dimensions

## Current Implementation

### Database Schema

Two separate BLOB columns for embeddings:
```sql
ALTER TABLE documents ADD COLUMN embedding BLOB;       -- nomic-embed-text (768 dims)
ALTER TABLE documents ADD COLUMN embedding_qwen BLOB;  -- qwen3-embedding (4096 dims)
```

### Usage

```bash
# Generate nomic embeddings (default)
./slab-search embed
./slab-search embed -model=nomic

# Generate qwen embeddings
./slab-search embed -model=qwen

# Search with nomic embeddings (default)
./slab-search search -semantic "kubernetes"
./slab-search search -semantic "kubernetes" -model=nomic

# Search with qwen embeddings
./slab-search search -semantic "kubernetes" -model=qwen

# Hybrid search with qwen
./slab-search search -hybrid=0.3 "kubernetes" -model=qwen
```

### Architecture

- **Storage**: Each model's embeddings stored in separate column
- **Embedding generation**: Model flag selects which Ollama model to use
- **Search**: Model flag selects which embedding column to read
- **Independence**: Can generate/regenerate embeddings for each model independently

## Model Comparison

### Available Models (Ollama)

| Model | Dimensions | Model Size | Storage/Doc | Search Speed (10k docs) | Quality | Notes |
|-------|-----------|-----------|-------------|------------------------|---------|-------|
| **nomic-embed-text** | 768 | 274 MB | 3 KB | ~40ms | Excellent | Default, fast, retrieval-optimized |
| **qwen3-embedding:0.6b** | up to 1024 | ~450 MB | 4 KB | ~53ms | Good | Configurable dims, multilingual |
| **qwen3-embedding** (default) | up to 4096 | 4.7 GB | 16 KB | ~213ms | Best | Highest quality, MTEB rank #1 |
| **qwen3-embedding:8b** | up to 4096 | ~5 GB | 16 KB | ~213ms | Best | Same as default |
| **embeddinggemma** | 768 (128-768 MRL) | ~250 MB | 3 KB | ~40ms | Excellent | Google, supports MRL |
| **mxbai-embed-large** | 1024 | ~670 MB | 4 KB | ~53ms | Excellent | Beats OpenAI models |
| **all-minilm** | 384 | ~120 MB | 1.5 KB | ~20ms | Good | Smallest/fastest |

### Performance Implications

**Current (nomic-embed-text):**
- Embedding generation: ~8-12 min for 10k docs
- Search latency: ~40ms
- Storage: ~30 MB for 10k docs

**With qwen3-embedding:**
- Embedding generation: ~8-12 min (similar)
- Search latency: ~213ms (5.3x slower due to 4096 dims)
- Storage: ~160 MB for 10k docs

**With both models:**
- Total storage: ~190 MB for 10k docs
- Can A/B test quality vs speed tradeoff

### Search Speed Analysis

Brute-force cosine similarity is O(n × d):
```
Time = num_docs × dimensions × constant

nomic (768 dims):    10,000 × 768  = 7.68M ops → 40ms
qwen (4096 dims):    10,000 × 4096 = 40.96M ops → 213ms
all-minilm (384):    10,000 × 384  = 3.84M ops → 20ms
```

At 10k documents, brute-force is still acceptable. Beyond 50-100k docs, consider:
- Approximate nearest neighbor (ANN) indexing (HNSW, IVF)
- Vector databases (chromem-go, sqlite-vss)
- Dimension reduction techniques

## Model Quality Research

### Matryoshka Representation Learning (MRL)

**What is MRL?**
- Technique that allows variable-sized embeddings from a single model
- Early dimensions contain most important information
- Can truncate to any size without retraining

**Example (embeddinggemma with MRL):**
```go
fullEmbedding := []float32{...}  // 768 dims
fastEmbedding := fullEmbedding[:384]  // Truncate to 384 - still meaningful!
```

**Benefits:**
- Speed dial: 256 dims (3x faster) → 768 dims (best quality)
- No re-embedding needed - just truncate
- One generation, multiple speed/quality tradeoffs

**MRL-capable models:**
- embeddinggemma (128-768 dims)
- nomic-embed-text-v2-moe (supports MRL)

**Note**: Our current nomic-embed-text does NOT support MRL.

### Model Recommendations

**For speed (< 30ms search):**
- all-minilm (384 dims) - 2x faster than current
- Test quality first - may sacrifice relevance

**For balanced (40-50ms search):**
- nomic-embed-text (current) - proven quality
- embeddinggemma - newer, supports MRL for flexibility

**For maximum quality (accepting 200ms+ search):**
- qwen3-embedding (4096 dims) - MTEB multilingual #1
- Test on your specific documents

**For multilingual:**
- qwen3-embedding - trained on 100+ languages
- embeddinggemma - 100+ languages

### Migration Considerations

**Switching models requires:**
1. Pull new model: `ollama pull <model>`
2. Regenerate ALL embeddings: `./slab-search embed -model=<model>`
3. Cannot mix embeddings from different models in same search

**Time to regenerate:**
- ~8-12 minutes for 10k documents
- Similar across models (bottleneck is Ollama, not model size)

**Storage cost:**
- Keeping both: ~190 MB total (acceptable)
- Can delete old embeddings by setting column to NULL

## Testing Methodology

### A/B Testing Search Quality

```bash
# 1. Generate embeddings for both models
./slab-search embed -model=nomic  # Uses existing or regenerates
./slab-search embed -model=qwen   # Takes ~8-12 min

# 2. Compare on same queries
./slab-search search -semantic "database optimization" -model=nomic
./slab-search search -semantic "database optimization" -model=qwen

# 3. Test on different query types
# Exact match queries
./slab-search search -semantic "kubernetes deployment yaml" -model=nomic
./slab-search search -semantic "kubernetes deployment yaml" -model=qwen

# Conceptual queries
./slab-search search -semantic "how do we handle scaling issues" -model=nomic
./slab-search search -semantic "how do we handle scaling issues" -model=qwen

# Technical jargon
./slab-search search -semantic "postgres connection pooling" -model=nomic
./slab-search search -semantic "postgres connection pooling" -model=qwen

# 4. Measure relevance
# For each query, check if top 5 results are relevant
# Calculate precision@5 for each model
```

### Metrics to Track

**Quantitative:**
- Search latency (p50, p95, p99)
- Embedding generation time
- Storage usage
- Precision@K (how many of top K results are relevant)

**Qualitative:**
- Do results match intent?
- Are conceptual matches better with qwen?
- Is speed difference noticeable to users?

## Future Optimizations

### If Search Becomes Too Slow

**Option 1: Use smaller model**
- Switch to all-minilm (384 dims) - 2x faster
- Or embeddinggemma with truncation (384 dims)

**Option 2: Add ANN indexing**
- chromem-go (pure Go, HNSW index)
- sqlite-vss (SQLite extension)
- Both reduce search from O(n) to O(log n)

**Option 3: Quantization**
- Reduce float32 to int8
- 4x storage reduction
- Minimal quality loss

### If Quality Needs Improvement

**Option 1: Try qwen3-embedding**
- Already implemented - just run `embed -model=qwen`
- MTEB #1 for multilingual

**Option 2: Fine-tuning**
- Use your Slab docs to fine-tune model
- Requires more infrastructure

**Option 3: Hybrid tuning**
- Adjust keyword/semantic weights
- Currently 70/30, could experiment with 50/50, 80/20

## Bleve Vector Support

**Status**: ✅ **Bleve v2.4.0+ has native vector search using FAISS!**

### Key Features

Bleve supports vector/KNN search with the following capabilities:

- **Similarity metrics**: `cosine` (v2.4.3+), `dot_product`, `l2_norm` (Euclidean)
- **Dimensions**: Up to 4096 (perfect for qwen3-embedding!)
- **Hybrid search**: Built-in text + vector combination
- **Pre-filtering**: Apply text filters before KNN (faster, more relevant)
- **Index optimizations**: `latency`, `memory_efficient`, `recall`
- **Uses FAISS**: Facebook AI Similarity Search (ANN, not brute-force!)

### Architecture

Bleve embeds FAISS indexes within scorch segments:
```
┌─────────────────────────────────┐
│     Bleve Index (scorch)        │
│  ┌──────────────┬──────────────┐│
│  │ Text Index   │ FAISS Vector ││
│  │ (BM25)       │ (ANN/HNSW)   ││
│  └──────────────┴──────────────┘│
└─────────────────────────────────┘
```

### Pros vs Current Brute-Force

| Feature | Current (Brute-Force) | Bleve + FAISS |
|---------|----------------------|---------------|
| **Search speed** | O(n) - 40ms @ 10k docs | O(log n) - <5ms @ 10k docs |
| **Scalability** | Slows linearly with docs | Sub-linear growth |
| **Hybrid search** | Custom merging | Built-in, optimized |
| **Index size** | No overhead | FAISS index overhead |
| **Setup** | Simple (pure Go) | Requires FAISS (C++) |

### Cons / Complexity

**Build requirements:**
- Must compile FAISS C++ library
- Build with `-tags=vectors` flag
- FAISS shared library must be accessible

**Setup on Linux:**
```bash
git clone https://github.com/blevesearch/faiss.git
cd faiss
cmake -B build -DFAISS_ENABLE_GPU=OFF -DFAISS_ENABLE_C_API=ON -DBUILD_SHARED_LIBS=ON .
make -C build
sudo make -C build install
sudo cp build/c_api/libfaiss_c.so /usr/local/lib
```

**Then rebuild with vectors:**
```bash
go build -tags=vectors -o slab-search ./cmd/slab-search
```

### Performance Comparison

**Current (10k docs, qwen 4096 dims):**
- Brute-force: ~213ms
- Linear growth: 100k docs → 2.1 seconds

**With Bleve + FAISS (estimated):**
- HNSW index: ~5-10ms
- Sub-linear growth: 100k docs → ~20-30ms

### Example Usage

```go
// Index with vector field
vectorFieldMapping := bleve.NewVectorFieldMapping()
vectorFieldMapping.Dims = 4096  // qwen dimensions
vectorFieldMapping.Similarity = "cosine"

bleveMapping.DefaultMapping.AddFieldMappingsAt("embedding", vectorFieldMapping)

// Hybrid search: text + vector
hybridRequest := bleve.NewSearchRequest(
    bleve.NewMatchQuery("kubernetes"))  // Text query
hybridRequest.AddKNN(
    "embedding",        // Vector field
    queryEmbedding,     // Query vector (4096 dims)
    10,                 // K nearest neighbors
    0.3,                // Vector boost (30% semantic, 70% keyword)
)

results, _ := index.Search(hybridRequest)
```

### Recommendation

**For current scale (10k docs):**
- ✅ **Keep brute-force** - simpler, no dependencies
- 40ms is acceptable, 213ms (qwen) is borderline

**If scaling to 50k+ docs OR choosing qwen:**
- ✅ **Switch to Bleve + FAISS**
- ~10x faster search
- Better user experience with qwen (213ms → 20ms)

**Implementation effort:**
- FAISS setup: 1-2 hours
- Code migration: 2-3 hours
- Testing: 1-2 hours
- **Total: 4-7 hours**

### Migration Path

**Phase 1: Keep current implementation**
- Works well for 10k docs with nomic (40ms)
- Simple, no dependencies

**Phase 2: Add Bleve vector support (when needed)**
- Triggered by: scaling to 50k+ docs OR switching to qwen
- Estimated effort: 4-7 hours
- Benefits: 5-10x faster search, better UX

### Resources

- [Bleve Vectors Documentation](https://github.com/blevesearch/bleve/blob/master/docs/vectors.md)
- [FAISS Setup](https://github.com/blevesearch/faiss/blob/main/INSTALL.md)
- Bleve vector support: v2.4.0+

## References

- [Ollama Embedding Models](https://ollama.com/blog/embedding-models)
- [Qwen3-Embedding GitHub](https://github.com/QwenLM/Qwen3-Embedding)
- [EmbeddingGemma](https://ollama.com/library/embeddinggemma)
- [Nomic Embed Text](https://ollama.com/library/nomic-embed-text)
- [MTEB Leaderboard](https://huggingface.co/spaces/mteb/leaderboard) - Embedding model benchmarks

## Known Issues & Fixes

### Qwen Timeout Issues

**Problem**: qwen3-embedding is a large model (4.7GB) and can take longer than 60s to generate embeddings for large documents.

**Symptom**:
```
Warning: Failed to generate embedding: context deadline exceeded (Client.Timeout exceeded while awaiting headers)
```

**Fix**: Increased timeout to 3 minutes for qwen models in `internal/embeddings/ollama.go`:
```go
// Automatically uses 3min timeout for qwen models
embedder := embeddings.NewClient("http://localhost:11434", "qwen3-embedding")
```

**Models affected**: qwen3-embedding, qwen3-embedding:8b, qwen3-embedding:4b

## Decision Log

**2025-10-13**: Implemented dual embedding support
- Added `embedding_qwen` column for A/B testing
- Chose qwen3-embedding for comparison (MTEB #1, multilingual)
- Kept nomic-embed-text as default (proven, fast)
- Strategy: Generate both, measure quality difference, decide whether to keep qwen or switch
- **Fixed**: Increased qwen timeout from 60s to 3min to handle large model inference time

**Key question to answer**: Is qwen's quality improvement worth the 5x search slowdown?
- If yes → keep qwen, potentially add ANN indexing later
- If no → stay with nomic, remove qwen embeddings
- Alternative → switch to embeddinggemma (same speed, newer model, MRL flexibility)
