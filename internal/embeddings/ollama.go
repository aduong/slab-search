package embeddings

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

// Client represents an Ollama embedding client
type Client struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewClient creates a new Ollama embedding client
func NewClient(baseURL, model string) *Client {
	// Set timeout based on model size
	// Larger models (qwen, etc.) need more time to generate embeddings
	timeout := 60 * time.Second // Default for small models (nomic-embed-text)

	// Increase timeout for large models
	if model == "qwen3-embedding" || model == "qwen3-embedding:latest" ||
	   model == "qwen3-embedding:8b" || model == "qwen3-embedding:4b" {
		timeout = 3 * time.Minute // 3 minutes for large qwen models
	}

	return &Client{
		baseURL: baseURL,
		model:   model,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// embedRequest is the request format for Ollama's /api/embed endpoint
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embedResponse is the response format from Ollama's /api/embed endpoint
type embedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed generates an embedding for a single text string
func (c *Client) Embed(text string) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}

	// Create request
	req := embedRequest{
		Model: c.model,
		Input: []string{text},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Make HTTP request
	resp, err := c.client.Post(c.baseURL+"/api/embed", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var embedResp embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(embedResp.Embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}

	return embedResp.Embeddings[0], nil
}

// EmbedBatch generates embeddings for multiple text strings in a single request
// This is more efficient than calling Embed() multiple times
func (c *Client) EmbedBatch(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("texts cannot be empty")
	}

	// Create request
	req := embedRequest{
		Model: c.model,
		Input: texts,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Make HTTP request
	resp, err := c.client.Post(c.baseURL+"/api/embed", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var embedResp embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(embedResp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(embedResp.Embeddings))
	}

	return embedResp.Embeddings, nil
}

// SerializeEmbedding converts a float32 vector to bytes for SQLite storage
// Uses little-endian encoding for portability
func SerializeEmbedding(vec []float32) []byte {
	buf := make([]byte, len(vec)*4) // 4 bytes per float32
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// DeserializeEmbedding converts bytes back to a float32 vector
func DeserializeEmbedding(data []byte) []float32 {
	if len(data) == 0 || len(data)%4 != 0 {
		return nil
	}

	vec := make([]float32, len(data)/4)
	for i := range vec {
		bits := binary.LittleEndian.Uint32(data[i*4:])
		vec[i] = math.Float32frombits(bits)
	}
	return vec
}

// CosineSimilarity computes the cosine similarity between two vectors
// Returns a value between -1 and 1, where 1 means identical direction
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float32
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}

// Health checks if the Ollama service is available and the model is loaded
func (c *Client) Health() error {
	resp, err := c.client.Get(c.baseURL + "/api/tags")
	if err != nil {
		return fmt.Errorf("ollama not available: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	// Parse response to check if model exists
	var tagsResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return fmt.Errorf("decode tags response: %w", err)
	}

	// Get base model name (strip tag suffix like :latest)
	wantedModel := stripModelTag(c.model)

	// Check if our model is available (compare base names without tags)
	for _, model := range tagsResp.Models {
		if stripModelTag(model.Name) == wantedModel {
			return nil // Model is available
		}
	}

	return fmt.Errorf("model %s not found (run: ollama pull %s)", c.model, c.model)
}

// stripModelTag removes the tag suffix from a model name (e.g., "model:latest" -> "model")
func stripModelTag(modelName string) string {
	if idx := len(modelName) - 1; idx >= 0 {
		for i := 0; i <= idx; i++ {
			if modelName[i] == ':' {
				return modelName[:i]
			}
		}
	}
	return modelName
}
