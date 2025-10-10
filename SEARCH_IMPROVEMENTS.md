# Search Quality Improvements

This document tracks search quality improvements, implementation tradeoffs, and future approaches.

## Implemented: Analyzer Improvements (2025-10-09)

### Changes Made

**Content Field:**
```go
contentFieldMapping := bleve.NewTextFieldMapping()
contentFieldMapping.Analyzer = "en"  // English analyzer
```

**Benefits:**
- **Stemming**: "deploy", "deploys", "deploying", "deployment" all match
- **Stopword removal**: Ignores "the", "a", "is", "an", etc.
- **Better tokenization**: Handles English text properly
- **Case normalization**: "Kubernetes" matches "kubernetes"

**Title Field:**
```go
titleFieldMapping := bleve.NewTextFieldMapping()
titleFieldMapping.Analyzer = "en"  // Same as content
```

**Benefits:**
- Same stemming and stopword benefits
- More forgiving title matching
- Finds documents even with word variations in title

**Author Field:**
```go
authorFieldMapping := bleve.NewTextFieldMapping()
// Default analyzer (no stemming)
```

**Rationale:**
- Names shouldn't be stemmed ("Van" in "Van Nguyen" should stay)
- No stopword removal for names
- Simple tokenization on whitespace is sufficient
- Case-insensitive matching is all we need

### Testing

```bash
# Before analyzer improvements:
./slab-search search "deploying"
# Only matches exact "deploying"

# After analyzer improvements:
./slab-search reindex
./slab-search search "deploying"
# Now matches: deploy, deploys, deployed, deploying, deployment

# Stemming examples:
./slab-search search "databases"  # Matches "database"
./slab-search search "running"    # Matches "run", "runs", "running"
./slab-search search "configured" # Matches "configure", "configuration"
```

### Performance Impact

- **Reindex time**: ~8 seconds (same as before)
- **Search time**: No degradation
- **Index size**: Slightly smaller (stopwords removed)

## Implemented: Title Boosting (2025-10-10)

### Implementation Approach

**Goal**: Make title matches rank 3x higher than content matches while preserving QueryStringQuery features

**Solution**: Hybrid query combining MatchQuery (title, with boost) and QueryStringQuery (content, with all features)

```go
// Title query: MatchQuery with boost
titleQuery := bleve.NewMatchQuery(queryStr)
titleQuery.SetField("Title")
titleQuery.SetBoost(3.0)

// Content query: QueryStringQuery (supports fuzzy, phrases, boolean ops)
contentQuery := bleve.NewQueryStringQuery(queryStr)

// Combine with OR (disjunction)
query := bleve.NewDisjunctionQuery(titleQuery, contentQuery)
```

### Why This Works

**Key Discovery**: We don't need to wrap the QueryStringQuery in `Content:(...)` because:
1. QueryStringQuery searches all fields by default
2. The DisjunctionQuery (OR) combines scores from both queries
3. Title matches get 3x boost from the MatchQuery component
4. Content matches use normal scoring from QueryStringQuery component
5. Documents can match on title, content, or both

### Features Preserved

✅ **All QueryStringQuery features work:**
- **Fuzzy search**: `architecture~` finds "architectural"
- **Phrase search**: `"render architecture"` finds exact phrase
- **Boolean operators**: `kubernetes AND postgres`, `redis OR memcached`
- **Exclusions**: `kubernetes -docker`
- **Wildcards**: `deploy*` finds "deployment"
- **Field-specific**: `author:john` (though author isn't in our default search)

✅ **Title boosting works:**
- Documents with query terms in title rank 3x higher
- "Render Architecture" now ranks #1 for `"render architecture"` query
- "Solutioning & Architecture" ranks #1 for `"architecture"` query

### Testing Results

**Before title boosting:**
```bash
./slab-search search "render architecture"
# Result #1: [draft] Customer Architecture Review/Deep Dive (0.638)
# "Render Architecture" not in top 5
```

**After title boosting:**
```bash
./slab-search search "render architecture"
# Result #1: Render Architecture (5.311) ✅
# Result #2: Render Architecture (Copy) (4.341)
# Result #3: Render Architecture Diagram (4.326)
```

**Score comparison:**
- Without boost: Content-heavy documents scored ~0.6-0.7
- With boost: Title matches now score 5.3+ (7-8x improvement)

### Alternative Approaches Considered

We evaluated several approaches before settling on the hybrid solution:

**Approach 1: SetBoost() on QueryStringQuery**
```go
q := bleve.NewQueryStringQuery(queryStr)
q.SetBoost(3.0)
```
- ❌ **Doesn't work**: SetBoost() is ignored on QueryStringQuery

**Approach 2: Field boost syntax**
```go
boostedQuery := "Title:render^3 Title:architecture^3 Content:render Content:architecture"
```
- ⚠️ **Works but requires parsing**: Need to tokenize and rebuild query
- ❌ **Breaks with fuzzy**: `Title:architecture~^3` causes parse errors
- ❌ **Complex edge cases**: Boolean operators, phrases, wildcards all need special handling

**Approach 3: Wrapped field syntax**
```go
boostedQuery := fmt.Sprintf("Title:(%s)^3 Content:(%s)", userQuery, userQuery)
```
- ⚠️ **Works but mediocre results**: Scores ~1.1 vs 5.3 with per-term boost
- ❌ **Still breaks with fuzzy**: `Content:(architecture~)` causes parse errors

**Approach 4: Post-process scoring**
```go
// Run search, then multiply scores if title matched
for _, hit := range results.Hits {
    if titleMatched(hit) {
        hit.Score *= 3.0
    }
}
```
- ✅ **Preserves all features**
- ❌ **Hacky**: Manual score manipulation
- ❌ **Inefficient**: Need to fetch 2x results, then re-sort

**Approach 5: Hybrid MatchQuery + QueryStringQuery (CHOSEN)**
```go
titleQuery := bleve.NewMatchQuery(queryStr)
titleQuery.SetBoost(3.0)
contentQuery := bleve.NewQueryStringQuery(queryStr)
combined := bleve.NewDisjunctionQuery(titleQuery, contentQuery)
```
- ✅ **Clean implementation**: No string manipulation
- ✅ **All QueryStringQuery features work**: Fuzzy, phrases, boolean ops
- ✅ **Excellent results**: Same scores as per-term boost approach
- ✅ **Simple**: Just 4 lines of code

### Performance Impact

- **Search time**: No measurable degradation
- **Implementation**: Simple, maintainable code
- **Features**: All preserved

## Future Improvements

Now that title boosting is implemented, here are other potential improvements:

### Option 1: Query Syntax with Field Boost

**Approach**: Use Bleve's field boost syntax in query string
```go
// User query: "kubernetes"
// Rewrite to: "Title:kubernetes^3 OR Content:kubernetes OR Author:kubernetes"

func (i *Index) Search(queryStr string, limit int) ([]*SearchResult, error) {
    // Expand query to include field boosts
    expandedQuery := fmt.Sprintf("Title:%s^3 OR Content:%s OR Author:%s",
        queryStr, queryStr, queryStr)

    query := bleve.NewQueryStringQuery(expandedQuery)
    // ... rest
}
```

**Pros:**
- Keeps QueryStringQuery features
- Title boost works

**Cons:**
- Doesn't work with complex user queries (phrase search, boolean operators)
- User types `"postgres config"` → becomes `Title:"postgres config"^3 OR ...` (breaks)
- Need to parse user query to detect quotes, operators, etc.

### Option 2: Custom Scoring Function

**Approach**: Post-process results to boost title matches
```go
func (i *Index) Search(queryStr string, limit int) ([]*SearchResult, error) {
    query := bleve.NewQueryStringQuery(queryStr)
    search := bleve.NewSearchRequestOptions(query, limit * 2, 0, false) // Get more results

    results, err := i.index.Search(search)

    // Re-score based on which field matched
    for _, hit := range results.Hits {
        if titleMatched(hit) {
            hit.Score *= 3.0
        }
    }

    // Re-sort and return top N
    sort.Slice(results.Hits, func(i, j int) bool {
        return results.Hits[i].Score > results.Hits[j].Score
    })

    return results.Hits[:limit], nil
}
```

**Pros:**
- Keeps QueryStringQuery features
- Full control over scoring
- Can implement complex boosting logic

**Cons:**
- Need to detect which field matched (check fragments/highlights)
- Re-sorting is extra work
- Must fetch more results than needed
- Score manipulation feels hacky

### Option 3: Bleve Plugins / Custom Scoring

**Approach**: Use Bleve's plugin system for custom scoring
```go
// Register custom scorer
type TitleBoostScorer struct {
    // Implement bleve.Scorer interface
}

func (s *TitleBoostScorer) Score(constituents []*search.DocumentMatch) float64 {
    // Custom scoring logic with title boost
}
```

**Pros:**
- Proper integration with Bleve
- Clean implementation
- Best performance

**Cons:**
- Complex implementation
- Requires deep Bleve knowledge
- May break on Bleve upgrades

### Option 4: Build Multiple Queries with Proper Parsing

**Approach**: Parse user query to preserve features, then build per-field queries
```go
func (i *Index) Search(queryStr string, limit int) ([]*SearchResult, error) {
    // Detect query type
    if strings.Contains(queryStr, "\"") {
        // Phrase search - use PhraseQuery with field boosts
        return i.searchPhrase(queryStr, limit)
    } else if strings.Contains(queryStr, "~") {
        // Fuzzy search - use FuzzyQuery with field boosts
        return i.searchFuzzy(queryStr, limit)
    } else if strings.ContainsAny(queryStr, "+-") {
        // Boolean - more complex parsing
        return i.searchBoolean(queryStr, limit)
    } else {
        // Simple match - use boosted field queries
        return i.searchSimple(queryStr, limit)
    }
}
```

**Pros:**
- Preserves most features
- Title boost works
- Can handle common cases

**Cons:**
- Very complex implementation
- Can't handle all query combinations
- Maintenance burden
- Edge cases everywhere

### Option 5: Hybrid Approach - Smart Detection

**Approach**: Use QueryStringQuery by default, boost only for simple queries
```go
func (i *Index) Search(queryStr string, limit int) ([]*SearchResult, error) {
    // If simple query (no special chars), use boosted field queries
    if isSimpleQuery(queryStr) {
        return i.searchWithBoost(queryStr, limit)
    }

    // For complex queries, use QueryStringQuery (no boost)
    query := bleve.NewQueryStringQuery(queryStr)
    // ... standard search
}

func isSimpleQuery(q string) bool {
    // No quotes, no ~, no +/-, no :, no wildcards
    return !strings.ContainsAny(q, "\"~+-:*")
}
```

**Pros:**
- Best of both worlds for most queries
- Simple queries get boosting
- Complex queries still work

**Cons:**
- Inconsistent behavior (some queries boosted, some not)
- User confusion about why results differ
- Still doesn't handle all cases

## Current Search Quality

**Implemented improvements:**
1. ✅ **English analyzer** with stemming and stopword removal
2. ✅ **Title boosting** (3x) using hybrid query approach

**Search quality is now excellent:**
- Title matches rank appropriately high
- All QueryStringQuery features preserved (fuzzy, phrases, boolean)
- Stemming finds word variations automatically
- Clean, maintainable implementation

**Next steps to consider:**
- Monitor user feedback on search relevance
- Track which queries don't return good results
- Consider author/date filtering in UI
- Possibly add automatic fuzzy matching for single-term queries

## Metrics to Track

To measure search quality improvements:

1. **Relevance**: Is the top result what you wanted?
2. **Coverage**: Does search find documents with word variations?
3. **Precision**: Are results actually relevant?
4. **User satisfaction**: Do users click top results?

## Alternative: Query Time Fuzzy

Instead of title boosting, we could enable fuzzy search by default:

```go
func (i *Index) Search(queryStr string, limit int) ([]*SearchResult, error) {
    // Add automatic fuzziness to all terms (unless phrase search)
    if !strings.Contains(queryStr, "\"") {
        query := bleve.NewQueryStringQuery(queryStr)
        query.SetFuzziness(1)  // Auto-fuzzy with edit distance 1
    } else {
        query := bleve.NewQueryStringQuery(queryStr)
    }
    // ...
}
```

This makes typo-tolerance automatic without losing QueryStringQuery features.

## Conclusion

We implemented analyzer improvements for significant search quality gains while preserving all QueryStringQuery features. Title boosting is possible but requires tradeoffs. The current implementation is clean, maintainable, and provides good search quality. Further improvements should be driven by actual user feedback and measured search quality metrics.
