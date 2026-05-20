package llm

import (
	"net"
	"net/http"
	"strings"
	"time"

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
//
// HTTP transport:
//   - DisableKeepAlives so a stale connection (after machine sleep or an
//     Ollama restart) can't cause silent hangs.
//   - 5s dial timeout so DNS/network failures surface fast.
//   - 5m response-header timeout — Ollama can take a while to return the
//     first byte on a cold model load (especially for larger models). Once
//     headers arrive, StreamIdleTimeout takes over.
//
// Resilience:
//   - StreamIdleTimeout=60s — if Ollama goes quiet mid-stream, the watchdog
//     cancels the request ctx and Stream.Next() returns ErrStreamIdle.
//   - RequestTimeout=0 — rely on ctx + watchdog, never the blanket http.Client
//     timeout (which would cut off legitimately slow generations).
func New(model, host string) (*Client, error) {
	if host == "" {
		host = "http://localhost:11434"
	}
	host = strings.TrimRight(host, "/")
	if !strings.HasSuffix(host, "/v1") {
		host += "/v1"
	}

	hc := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			DisableKeepAlives:     true,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			ResponseHeaderTimeout: 5 * time.Minute,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
	res := litellm.DefaultResilienceConfig()
	res.StreamIdleTimeout = 60 * time.Second
	res.RequestTimeout = 0

	c, err := litellm.NewWithProvider("ollama", litellm.ProviderConfig{
		BaseURL:    host,
		APIKey:     "ollama",
		HTTPClient: hc,
		Resilience: res,
	})
	if err != nil {
		return nil, err
	}
	return &Client{Client: c, Model: model}, nil
}
