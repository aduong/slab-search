# Web Frontend Implementation

This document describes the web frontend implementation for Slab Search, including architecture decisions, usage, and implementation details.

## Overview

The web frontend provides a modern, responsive search interface for Slab documents. It's built with:
- **Go templates** for server-side HTML rendering
- **HTMX** for dynamic, real-time search without full page reloads
- **Vanilla CSS** for styling (no framework dependencies)
- **Embedded assets** for single-binary deployment

## Architecture

### Server-Side Rendering with HTMX

We chose **server-side rendering with HTMX** over a traditional SPA approach for several reasons:

1. **Simplicity**: No complex JavaScript framework or build pipeline
2. **Performance**: HTML fragments are smaller than JSON + client-side rendering
3. **Single Binary**: Assets embed in the Go binary using `embed.FS`
4. **SEO-Friendly**: Server renders actual HTML (though not critical for internal tool)
5. **Progressive Enhancement**: Works without JavaScript (degrades gracefully)

### Request Flow

```
User types in search box
    ↓
HTMX debounces (300ms)
    ↓
GET /api/search?q=kubernetes&mode=keyword
    ↓
Server performs search (Bleve/Semantic)
    ↓
Server renders HTML fragments
    ↓
HTMX swaps HTML into #results div
    ↓
CSS transitions provide smooth updates
```

### File Structure

```
internal/web/
├── server.go              # HTTP server and handlers
├── templates/
│   └── index.html         # Main search UI template
└── static/
    └── style.css          # Styling (embedded in binary)
```

## Components

### 1. HTTP Server (`server.go`)

The server exposes three endpoints:

#### `GET /` - Main Search Page
Returns the full HTML page with search interface.

```go
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request)
```

**Template Data:**
- `HasEmbeddings`: Boolean indicating if semantic/hybrid search is available

#### `GET /api/search` - Search API
Performs search and returns HTML fragments (not JSON).

**Query Parameters:**
- `q`: Search query (required)
- `mode`: Search mode (`keyword`, `semantic`, `hybrid`)
- `limit`: Max results (default: 20, max: 100)
- `weight`: Semantic weight for hybrid mode (0.0-1.0, default: 0.3)

**Response:** HTML fragment containing:
- Results header with count and mode
- Result cards with title, author, preview, score
- Empty state or error messages

#### `GET /health` - Health Check
Returns JSON with system status.

```json
{
  "status": "ok",
  "documents_in_db": 10023,
  "documents_in_index": 10023,
  "embeddings_available": true
}
```

### 2. HTML Template (`index.html`)

**Key Features:**

#### Search Input
```html
<input type="text" id="searchInput" name="q"
    hx-get="/api/search"
    hx-trigger="keyup changed delay:300ms, search"
    hx-target="#results"
    hx-include="[name='mode']"
    hx-indicator="#loading">
```

- `hx-trigger`: Debounces input by 300ms
- `hx-target`: Swaps response into `#results` div
- `hx-include`: Includes search mode radio buttons
- `hx-indicator`: Shows loading spinner during request

#### Search Mode Toggles
Radio buttons for keyword/hybrid/semantic search:

```html
<label class="search-mode">
    <input type="radio" name="mode" value="keyword" checked
        hx-trigger="change"
        hx-get="/api/search"
        hx-include="#searchInput"
        hx-target="#results">
    <span>Keyword</span>
</label>
```

Mode changes immediately trigger a new search.

#### Results Container
```html
<div id="results" class="results">
    <!-- HTMX swaps HTML fragments here -->
</div>
```

#### Keyboard Shortcut
Press `/` to focus the search input (like GitHub, Slack, etc.):

```javascript
document.addEventListener('keydown', function(e) {
    if (e.key === '/' && document.activeElement.tagName !== 'INPUT') {
        e.preventDefault();
        document.getElementById('searchInput').focus();
    }
});
```

### 3. CSS Styling (`style.css`)

**Design System:**
- **Color Palette**: CSS variables for theming
- **Typography**: System font stack for native look
- **Shadows**: Subtle elevation with `box-shadow`
- **Transitions**: Smooth hover/focus states
- **Responsive**: Mobile-first with `@media` queries

**Key CSS Variables:**
```css
:root {
    --primary: #2563eb;
    --primary-dark: #1e40af;
    --border: #e5e7eb;
    --bg-gray: #f9fafb;
    --text-primary: #111827;
    --text-secondary: #6b7280;
}
```

**Result Card Structure:**
```
┌────────────────────────────────────┐
│ [1] │ Title (clickable link)       │
│     │ By Author Name               │
│     │ Preview text with <mark>...  │
│     │ Score: 0.576 | Open in Slab →│
└────────────────────────────────────┘
```

## Usage

### Starting the Server

```bash
# Default (localhost:8080)
./slab-search serve

# Custom port
./slab-search serve -port=3000

# Custom host (e.g., for Docker)
./slab-search serve -host=0.0.0.0 -port=8080
```

### Search Modes

**Keyword** (default):
- Bleve full-text search
- Fuzzy matching with `~` suffix
- Phrase search with quotes
- Boolean operators (AND/OR/NOT)

**Hybrid** (70% keyword, 30% semantic):
- Combines keyword and semantic results
- Merges scores with weighted average
- Best of both approaches

**Semantic** (requires embeddings):
- Pure cosine similarity search
- Finds conceptually related content
- Works well for abstract queries

### URL Examples

```bash
# Keyword search
http://localhost:8080/api/search?q=kubernetes&mode=keyword

# Hybrid search
http://localhost:8080/api/search?q=database+scaling&mode=hybrid

# Semantic search
http://localhost:8080/api/search?q=how+to+debug+memory+leaks&mode=semantic

# Custom limit
http://localhost:8080/api/search?q=redis&limit=50
```

## Implementation Notes

### Why HTML Fragments Instead of JSON?

**Original Approach (JSON + Client-Side Rendering):**
```javascript
// Server returns JSON
{ "results": [...], "count": 10 }

// Client parses and renders
response.results.forEach(result => {
    html += `<div>${escapeHtml(result.title)}</div>`;
});
```

**Current Approach (HTML Fragments):**
```go
// Server renders HTML
fmt.Fprintf(w, `<div class="result-card">
    <h3><a href="%s">%s</a></h3>
</div>`, template.HTMLEscapeString(url), template.HTMLEscapeString(title))
```

**Advantages:**
- No JSON parsing overhead
- HTML escaping handled server-side (security)
- Smaller payload (no field names repeated)
- Direct DOM manipulation by HTMX (faster)
- No JavaScript errors from malformed responses

### Security Considerations

**XSS Prevention:**
All user input is escaped using `template.HTMLEscapeString()`:

```go
fmt.Fprintf(w, `<h3>%s</h3>`, template.HTMLEscapeString(result.Title))
```

**Exception:** Search result previews use `template.HTML()` because they contain intentional `<mark>` tags from Bleve:

```go
fmt.Fprintf(w, `<p>%s</p>`, template.HTML(preview))
```

This is safe because:
1. Preview text comes from our own database (not user input)
2. Bleve only adds `<mark>` tags (controlled, safe HTML)
3. Original content was sanitized during sync

**CSRF Protection:**
Not needed for GET requests (search is idempotent). If we add POST endpoints (e.g., saved searches), we'll need CSRF tokens.

### Performance Optimizations

**1. Debouncing (300ms):**
```html
hx-trigger="keyup changed delay:300ms"
```
Prevents excessive requests while typing.

**2. HTTP Caching:**
Could add `Cache-Control` headers for identical queries:
```go
w.Header().Set("Cache-Control", "private, max-age=60")
```
Not implemented yet (search is fast enough).

**3. Result Limiting:**
Default 20 results, max 100. Prevents loading huge result sets.

**4. Index Not Locked:**
Bleve index is opened read-only, allowing concurrent searches.

### Concurrency

The server can handle multiple concurrent searches:

```go
// Multiple goroutines can call this simultaneously
results, err := idx.Search(query, limit)
```

Bleve's `index.Search()` is thread-safe. SQLite is opened in read-only mode for semantic search, which also allows concurrent reads.

### Future Enhancements

**Potential Improvements:**
- [ ] Pagination or infinite scroll for large result sets
- [ ] Filter sidebar (author, date range, topics)
- [ ] Search query autocomplete/suggestions
- [ ] Result sorting options (date, relevance, title)
- [ ] Saved searches with shareable URLs
- [ ] Search history (localStorage)
- [ ] Dark mode toggle
- [ ] Export results to CSV/JSON

**Not Planned:**
- ❌ User authentication (internal tool, assumes trusted network)
- ❌ Complex SPA framework (HTMX is sufficient)
- ❌ Real-time collaboration features

## Deployment

### Local Development

```bash
# Build
go build -o slab-search ./cmd/slab-search

# Run
./slab-search serve

# Test
curl http://localhost:8080/health
```

### Production Deployment

**As a systemd service:**

```ini
# /etc/systemd/system/slab-search.service
[Unit]
Description=Slab Search Web Server
After=network.target

[Service]
Type=simple
User=slab-search
WorkingDirectory=/opt/slab-search
ExecStart=/opt/slab-search/slab-search serve -host=0.0.0.0 -port=8080
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

**Enable and start:**
```bash
sudo systemctl enable slab-search
sudo systemctl start slab-search
```

**Behind a reverse proxy (nginx):**

```nginx
server {
    listen 80;
    server_name slab-search.render.com;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

### Docker Deployment

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o slab-search ./cmd/slab-search

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/slab-search .
COPY --from=builder /app/data ./data
EXPOSE 8080
CMD ["./slab-search", "serve", "-host=0.0.0.0"]
```

**Run:**
```bash
docker build -t slab-search .
docker run -p 8080:8080 -v $(pwd)/data:/root/data slab-search
```

## Troubleshooting

### Server won't start

**Error: "Address already in use"**
```bash
# Find process using port 8080
lsof -i :8080

# Kill it or use different port
./slab-search serve -port=3000
```

**Error: "Error opening search index"**
- Check that `./data/bleve` directory exists
- Ensure no other process has the index locked
- Run `./slab-search reindex` to rebuild

### Search returns no results

1. Check index has documents:
   ```bash
   ./slab-search stats
   ```

2. Test with simple query:
   ```bash
   curl 'http://localhost:8080/api/search?q=the'
   ```

3. For semantic search, verify embeddings:
   ```bash
   sqlite3 data/slab.db "SELECT COUNT(*) FROM documents WHERE embedding IS NOT NULL;"
   ```

### Semantic search unavailable

**Error: "Ollama not running"**

1. Check Ollama status:
   ```bash
   curl http://localhost:11434/api/version
   ```

2. Start Ollama:
   ```bash
   ollama serve
   ```

3. Pull model if needed:
   ```bash
   ollama pull nomic-embed-text
   ```

4. Restart slab-search server

## Technical Decisions

### Why HTMX over React/Vue?

**Pros:**
- ✅ Zero build step (no webpack, vite, etc.)
- ✅ Minimal JavaScript (~14KB gzipped)
- ✅ Server controls rendering (easier to maintain)
- ✅ Progressive enhancement (works without JS)
- ✅ Faster time-to-interactive

**Cons:**
- ❌ Less suitable for complex interactions
- ❌ Smaller ecosystem than React
- ❌ Team may be less familiar

For a search interface, HTMX is perfect. We'd reconsider for a more complex dashboard.

### Why Go Templates over Templ?

[Templ](https://templ.guide/) is a modern Go templating language, but we chose standard `html/template` for:

- ✅ Standard library (no dependencies)
- ✅ Team familiarity
- ✅ Good enough for simple templates
- ✅ Built-in XSS protection

Templ would be worth considering for a larger project with many complex templates.

### Why Embed Assets?

```go
//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS
```

**Benefits:**
- Single binary deployment (just copy `slab-search`)
- No risk of missing asset files
- Simpler deployment (no asset versioning/CDN needed)
- Faster startup (no disk reads for assets)

**Drawback:**
- Must rebuild to update CSS/HTML (acceptable for internal tool)

## References

- [HTMX Documentation](https://htmx.org/docs/)
- [Go Templates](https://pkg.go.dev/html/template)
- [Go Embed](https://pkg.go.dev/embed)
- [Bleve Search](https://blevesearch.com/docs/)
