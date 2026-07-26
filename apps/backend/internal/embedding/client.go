// Package embedding wraps an external embedding provider so the rest of the
// app can ask for "vector for these strings" without caring which SDK is
// underneath. Today only OpenAI is wired; the Client interface keeps the
// door open for swap-ins.
package embedding

import (
	"context"
	"fmt"

	openai "github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
)

// Client is the narrow contract reindex and chat code call into.
type Client interface {
	// Embed returns one vector per input, in the same order. Implementations
	// should chunk internally if needed.
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
	Model() string
	Dim() int
}

// OpenAIClient calls the OpenAI /v1/embeddings endpoint via openai-go/v2.
type OpenAIClient struct {
	sdk       openai.Client
	model     string
	dim       int
	batchSize int
}

// NewOpenAI constructs a client. batchSize is the maximum number of inputs
// per HTTP request; OpenAI's hard ceiling is 2048. apiKey + baseURL are
// forwarded to the SDK. Pass baseURL = "" to use the OpenAI default.
func NewOpenAI(apiKey, baseURL, model string, dim, batchSize int) *OpenAIClient {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	if batchSize <= 0 || batchSize > 2048 {
		batchSize = 96
	}
	return &OpenAIClient{
		sdk:       openai.NewClient(opts...),
		model:     model,
		dim:       dim,
		batchSize: batchSize,
	}
}

func (c *OpenAIClient) Model() string { return c.model }
func (c *OpenAIClient) Dim() int      { return c.dim }

func (c *OpenAIClient) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(inputs))
	for i := 0; i < len(inputs); i += c.batchSize {
		end := i + c.batchSize
		if end > len(inputs) {
			end = len(inputs)
		}
		chunk := inputs[i:end]
		resp, err := c.sdk.Embeddings.New(ctx, openai.EmbeddingNewParams{
			Model: c.model,
			Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: chunk},
		})
		if err != nil {
			return nil, fmt.Errorf("openai embeddings chunk [%d:%d]: %w", i, end, err)
		}
		if len(resp.Data) != len(chunk) {
			return nil, fmt.Errorf("openai embeddings returned %d vectors for %d inputs", len(resp.Data), len(chunk))
		}
		// OpenAI returns ordered by Index but defensively sort by Index in case
		// the API ever ships them out of order.
		ordered := make([][]float64, len(resp.Data))
		for _, e := range resp.Data {
			if int(e.Index) < 0 || int(e.Index) >= len(ordered) {
				return nil, fmt.Errorf("openai embeddings index out of range: %d", e.Index)
			}
			ordered[e.Index] = e.Embedding
		}
		for _, v := range ordered {
			if c.dim > 0 && len(v) != c.dim {
				return nil, fmt.Errorf("embedding dim mismatch: got %d, want %d (model=%s)", len(v), c.dim, c.model)
			}
			f32 := make([]float32, len(v))
			for i, x := range v {
				f32[i] = float32(x)
			}
			out = append(out, f32)
		}
	}
	return out, nil
}
