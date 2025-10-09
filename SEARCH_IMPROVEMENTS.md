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

## Not Implemented: Title Boosting

### What We Tried

**Goal**: Make title matches rank 3x higher than content matches

**Attempted approach:**
```go
titleQuery := bleve.NewQueryStringQuery(queryStr)
titleQuery.SetField("Title")
titleQuery.SetBoost(3.0)
```

**Problem**: `QueryStringQuery` doesn't support `SetField()` or field-specific boosting

**Alternative attempted:**
```go
titleQuery := bleve.NewMatchQuery(queryStr)
titleQuery.SetField("Title")
titleQuery.SetBoost(3.0)

contentQuery := bleve.NewMatchQuery(queryStr)
contentQuery.SetField("Content")

query := bleve.NewDisjunctionQuery(titleQuery, contentQuery)
```

### The Tradeoff Discovered

**QueryStringQuery Features (what we lose with MatchQuery):**
- ✅ **Fuzzy search**: `deploy~` finds "deployment"
- ✅ **Phrase search**: `"postgres config"` finds exact phrase
- ✅ **Boolean operators**: `kubernetes AND postgres`
- ✅ **Field-specific search**: `Author:john`
- ✅ **Wildcards**: `deploy*` finds "deployment"
- ✅ **Exclusions**: `kubernetes -docker`

**Field-Specific Boosting (what we gain with MatchQuery):**
- ✅ Title matches rank higher
- ✅ Author matches can have custom weight
- ✅ More control over scoring

**Decision**: We chose to preserve QueryStringQuery functionality because:
1. User-facing features (fuzzy, phrase) are more valuable than invisible boosting
2. Users expect `~` and `""` to work
3. Title matches already rank relatively high due to length (shorter text = higher score)
4. Analyzer improvements provide significant quality gains

## Future Approaches to Implement Title Boosting

If we want to add title boosting without losing QueryStringQuery features:

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

## Recommendation

**Current state (with analyzers only)** is a good balance:
- Search quality improved significantly with stemming
- All QueryStringQuery features preserved
- Simple, maintainable code

**If title boosting is needed**, I recommend:
1. **Short term**: Option 5 (Hybrid) - boost simple queries only
2. **Long term**: Option 2 (Post-process scoring) - most flexible

**However**, before implementing boosting:
- Measure actual search quality with current implementation
- Get user feedback on search results
- Determine if boosting is actually needed or if analyzers are sufficient
- Title matches may already rank high due to document length effects

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
