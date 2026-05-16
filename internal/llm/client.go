package llm

import (
	"strings"

	"github.com/voocel/litellm"
)

// Client wraps a litellm client together with the default model name.
type Client struct {
	*litellm.Client
	Model string
}

// New constructs a litellm Ollama client pointed at host. host may be a bare
// origin (http://localhost:11434) or the full OpenAI-compatible base URL — the
// "/v1" suffix is added automatically.
func New(model, host string) (*Client, error) {
	if host == "" {
		host = "http://localhost:11434"
	}
	host = strings.TrimRight(host, "/")
	if !strings.HasSuffix(host, "/v1") {
		host += "/v1"
	}
	c, err := litellm.NewWithProvider("ollama", litellm.ProviderConfig{
		BaseURL: host,
		APIKey:  "ollama",
	})
	if err != nil {
		return nil, err
	}
	return &Client{Client: c, Model: model}, nil
}
