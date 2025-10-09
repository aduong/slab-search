# Database Migration Guide

## Phase 2 Migration: Adding Semantic Search Support

If you have an existing database from Phase 1 (before semantic search), you need to add the `embedding` column.

### Automatic Migration

**Good news**: The latest code includes automatic migration! Simply run any command and it will detect and add the missing column:

```bash
# This will automatically add the embedding column if it doesn't exist
./slab-search stats
# or
./slab-search sync
```

The migration code checks for the `embedding` column and adds it if missing. No manual intervention needed!

### Manual Migration (If Automatic Fails)

If for some reason the automatic migration doesn't work, you can add the column manually:

```bash
# Add the embedding column
sqlite3 data/slab.db "ALTER TABLE documents ADD COLUMN embedding BLOB;"

# Verify it was added
sqlite3 data/slab.db "PRAGMA table_info(documents);"
```

You should see `embedding` in the column list with type `BLOB`.

### Verification

Check that the column was added successfully:

```bash
# Check database schema
sqlite3 data/slab.db "PRAGMA table_info(documents);"

# Should show:
# 0|id|TEXT|0||1
# 1|title|TEXT|1||0
# 2|content|TEXT|1||0
# 3|author_name|TEXT|0||0
# 4|author_email|TEXT|0||0
# 5|slab_url|TEXT|1||0
# 6|topics|TEXT|0||0
# 7|published_at|TIMESTAMP|0||0
# 8|updated_at|TIMESTAMP|0||0
# 9|archived_at|TIMESTAMP|0||0
# 10|synced_at|TIMESTAMP|1||0
# 11|embedding|BLOB|0||0  <-- This line should be present
```

### After Migration

Once the `embedding` column exists, you can:

1. **Generate embeddings for existing documents**:
   ```bash
   # Make sure Ollama is running and model is installed
   ollama pull nomic-embed-text

   # Option A: Reindex (faster - uses existing database)
   ./slab-search reindex

   # Option B: Sync (slower - fetches from Slab)
   ./slab-search sync
   ```

   **Command comparison**:
   - `sync` - fetches from Slab + generates embeddings + indexes in Bleve (~10-15 min)
   - `reindex` - uses existing DB + generates embeddings + rebuilds index (~8-12 min, faster!)

2. **Use semantic search**:
   ```bash
   # Semantic search
   ./slab-search search -semantic "database scaling"

   # Hybrid search
   ./slab-search search -hybrid=0.3 "kubernetes"
   ```

## Common Issues

### Issue: "no such column: embedding"

**Cause**: The database was created before Phase 2 and automatic migration failed.

**Solution**:
```bash
# Add column manually
sqlite3 data/slab.db "ALTER TABLE documents ADD COLUMN embedding BLOB;"

# Rebuild the binary to ensure latest code
go build -o slab-search ./cmd/slab-search

# Try again
./slab-search stats
```

### Issue: "duplicate column name: embedding"

**Cause**: The column already exists (migration was successful).

**Solution**: No action needed! The column is already present.

### Issue: "Ollama not available"

**Cause**: Ollama service is not running or model not installed.

**Solution**:
```bash
# Install Ollama
curl -fsSL https://ollama.com/install.sh | sh

# Start Ollama
ollama serve
# or
systemctl start ollama

# Install embedding model
ollama pull nomic-embed-text

# Verify it's working
curl http://localhost:11434/api/tags
```

## Migration Code

The automatic migration code in `internal/storage/db.go`:

```go
func (d *DB) runMigrations() error {
    // Migration 1: Add embedding column (Phase 2 - Semantic Search)
    var columnExists bool
    err := d.db.QueryRow(`
        SELECT COUNT(*) > 0
        FROM pragma_table_info('documents')
        WHERE name='embedding'
    `).Scan(&columnExists)

    if err != nil {
        return fmt.Errorf("check embedding column: %w", err)
    }

    if !columnExists {
        _, err = d.db.Exec("ALTER TABLE documents ADD COLUMN embedding BLOB")
        if err != nil {
            return fmt.Errorf("add embedding column: %w", err)
        }
    }

    return nil
}
```

This code:
1. Checks if the `embedding` column exists
2. If not, adds it automatically
3. Is safe to run multiple times (idempotent)

## Future Migrations

For future schema changes, add new migrations to the `runMigrations()` function in `internal/storage/db.go`.

**TODO**: Implement a proper migration tracking system using:
- golang-migrate
- goose
- or custom migration version tracking

See `design.md` for details on planned migration system improvements.
