package embeddings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Ensure LMStudioClient implements Embedder interface at compile time
var _ Embedder = (*LMStudioClient)(nil)

// LMStudioClient represents an LMStudio embedding client using OpenAI-compatible API
type LMStudioClient struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewLMStudioClient creates a new LMStudio embedding client
func NewLMStudioClient(baseURL, model string) *LMStudioClient {
	return &LMStudioClient{
		baseURL: baseURL,
		model:   model,
		client: &http.Client{
			Timeout: 3 * time.Minute, // Generous timeout for large models
		},
	}
}

// openAIEmbedRequest is the request format for OpenAI-compatible /v1/embeddings endpoint
type openAIEmbedRequest struct {
	Input interface{} `json:"input"` // Can be string or []string
	Model string      `json:"model"`
}

// openAIEmbedResponse is the response format from OpenAI-compatible /v1/embeddings endpoint
type openAIEmbedResponse struct {
	Object string `json:"object"`
	Data   []struct {
		Object    string    `json:"object"`
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed generates an embedding for a single text string
func (c *LMStudioClient) Embed(text string) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}

	// Create request
	req := openAIEmbedRequest{
		Input: text,
		Model: c.model,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Make HTTP request
	resp, err := c.client.Post(c.baseURL+"/v1/embeddings", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("lmstudio error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var embedResp openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(embedResp.Data) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}

	return embedResp.Data[0].Embedding, nil
}

// EmbedBatch generates embeddings for multiple text strings in a single request
func (c *LMStudioClient) EmbedBatch(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("texts cannot be empty")
	}

	// Create request
	req := openAIEmbedRequest{
		Input: texts,
		Model: c.model,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Make HTTP request
	resp, err := c.client.Post(c.baseURL+"/v1/embeddings", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("lmstudio error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var embedResp openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(embedResp.Data) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(embedResp.Data))
	}

	// Extract embeddings in order
	result := make([][]float32, len(texts))
	for _, data := range embedResp.Data {
		if data.Index < 0 || data.Index >= len(texts) {
			return nil, fmt.Errorf("invalid embedding index: %d", data.Index)
		}
		result[data.Index] = data.Embedding
	}

	return result, nil
}

// Health checks if the LMStudio service is available
func (c *LMStudioClient) Health() error {
	// Try to get models list from OpenAI-compatible endpoint
	resp, err := c.client.Get(c.baseURL + "/v1/models")
	if err != nil {
		return fmt.Errorf("lmstudio not available: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("lmstudio returned status %d", resp.StatusCode)
	}

	// Parse response to check if our model exists
	var modelsResp struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return fmt.Errorf("decode models response: %w", err)
	}

	// Check if any model is loaded (LMStudio might not list exact model names)
	if len(modelsResp.Data) == 0 {
		return fmt.Errorf("no models loaded in lmstudio")
	}

	// If we have a specific model, check if it exists
	if c.model != "" {
		for _, model := range modelsResp.Data {
			if model.ID == c.model {
				return nil // Exact match found
			}
		}
		// Model not found, but LMStudio is available - just warn in logs later
		// We'll let it proceed since LMStudio might accept any model name
	}

	return nil // LMStudio is available with some model loaded
}
