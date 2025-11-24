package embeddings

import (
	"fmt"
)

// Embedder is the interface for embedding providers (Ollama, LMStudio, etc.)
type Embedder interface {
	// Embed generates an embedding for a single text string
	Embed(text string) ([]float32, error)

	// EmbedBatch generates embeddings for multiple text strings in a single request
	EmbedBatch(texts []string) ([][]float32, error)

	// Health checks if the service is available and the model is loaded
	Health() error
}

// NewEmbedder creates a new embedding client based on the provider type
// Supported providers: "ollama", "lmstudio"
func NewEmbedder(provider, baseURL, model string) (Embedder, error) {
	switch provider {
	case "ollama":
		return NewClient(baseURL, model), nil
	case "lmstudio":
		return NewLMStudioClient(baseURL, model), nil
	default:
		return nil, fmt.Errorf("unsupported embedding provider: %s (supported: ollama, lmstudio)", provider)
	}
}

// GetDefaultURL returns the default base URL for a given provider
func GetDefaultURL(provider string) string {
	switch provider {
	case "ollama":
		return "http://localhost:11434"
	case "lmstudio":
		return "http://localhost:1234"
	default:
		return ""
	}
}

// GetDefaultModel returns the default model name for a given provider
func GetDefaultModel(provider string) string {
	switch provider {
	case "ollama":
		return "nomic-embed-text"
	case "lmstudio":
		return "text-embedding-nomic-embed-text-v1.5"
	default:
		return ""
	}
}

// GetModelName maps a model flag (like "nomic" or "qwen") to the provider-specific model name
func GetModelName(provider, modelFlag string) string {
	switch provider {
	case "ollama":
		switch modelFlag {
		case "nomic":
			return "nomic-embed-text"
		case "qwen":
			return "qwen3-embedding"
		default:
			return modelFlag // Pass through unknown models
		}
	case "lmstudio":
		switch modelFlag {
		case "nomic":
			return "text-embedding-nomic-embed-text-v1.5"
		case "qwen":
			return "mungert/qwen3-embedding-4b-gguf/qwen3-embedding-4b-bf16_q8_0.gguf"
		default:
			return modelFlag // Pass through unknown models
		}
	default:
		return modelFlag
	}
}
