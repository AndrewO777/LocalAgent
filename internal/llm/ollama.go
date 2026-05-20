package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ContextProbe captures what we could learn about the effective context
// length Ollama will serve for a given model. EffectiveCtx is 0 when we
// couldn't determine it (typically: no num_ctx in the Modelfile, and the
// model isn't currently loaded). NativeMax is the model's trained ceiling.
type ContextProbe struct {
	EffectiveCtx int    // tokens Ollama will actually serve; 0 = unknown
	NativeMax    int    // model's trained max ctx (from GGUF metadata); 0 if unknown
	Source       string // "ps" | "show" | "unreachable" | "unknown"
	Note         string // human-readable explanation suitable for surfacing in UI
}

// ProbeContextLength tries to determine the effective num_ctx Ollama will
// use for the given model. It checks /api/ps first (the model may already be
// loaded, in which case we know the exact context length). Falls back to
// /api/show to read num_ctx from the Modelfile parameters.
//
// This is best-effort. Network failures yield EffectiveCtx=0 with an
// explanatory Note — callers should treat that as "couldn't tell, trust the
// user", not as an error.
//
// host may be either the bare origin (http://localhost:11434) or the
// OpenAI-compatible URL ending in /v1; the /v1 suffix is stripped because
// /api/ps and /api/show live on the native Ollama API.
func ProbeContextLength(host, model string) ContextProbe {
	host = strings.TrimRight(host, "/")
	host = strings.TrimSuffix(host, "/v1")
	host = strings.TrimRight(host, "/")
	if host == "" {
		host = "http://localhost:11434"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1) /api/ps — if the model is already loaded, this is the truth.
	if n := probePS(ctx, host, model); n > 0 {
		return ContextProbe{
			EffectiveCtx: n,
			Source:       "ps",
			Note:         fmt.Sprintf("model currently loaded with context_length=%d", n),
		}
	}

	// 2) /api/show — Modelfile parameters tell us what will be used at load.
	return probeShow(ctx, host, model)
}

func probePS(ctx context.Context, host, model string) int {
	req, err := http.NewRequestWithContext(ctx, "GET", host+"/api/ps", nil)
	if err != nil {
		return 0
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	var data struct {
		Models []struct {
			Name          string `json:"name"`
			Model         string `json:"model"`
			ContextLength int    `json:"context_length"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0
	}
	for _, m := range data.Models {
		if m.Name == model || m.Model == model {
			return m.ContextLength
		}
	}
	return 0
}

var numCtxRe = regexp.MustCompile(`(?m)^\s*num_ctx\s+(\d+)`)

func probeShow(ctx context.Context, host, model string) ContextProbe {
	body, _ := json.Marshal(map[string]string{"name": model})
	req, err := http.NewRequestWithContext(ctx, "POST", host+"/api/show", bytes.NewReader(body))
	if err != nil {
		return ContextProbe{Source: "unreachable", Note: "probe request build failed: " + err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ContextProbe{Source: "unreachable", Note: "ollama /api/show unreachable: " + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ContextProbe{Source: "unknown", Note: fmt.Sprintf("ollama /api/show returned HTTP %d", resp.StatusCode)}
	}
	var data struct {
		Parameters string         `json:"parameters"`
		ModelInfo  map[string]any `json:"model_info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return ContextProbe{Source: "unknown", Note: "ollama /api/show decode failed: " + err.Error()}
	}

	nativeMax := parseNativeContext(data.ModelInfo)

	if n := ParseNumCtxParameter(data.Parameters); n > 0 {
		return ContextProbe{
			EffectiveCtx: n,
			NativeMax:    nativeMax,
			Source:       "show",
			Note:         fmt.Sprintf("Modelfile sets num_ctx=%d (model native max=%d)", n, nativeMax),
		}
	}

	// No explicit num_ctx — Ollama will use its server-side default.
	return ContextProbe{
		EffectiveCtx: 0,
		NativeMax:    nativeMax,
		Source:       "show",
		Note:         fmt.Sprintf("model has no num_ctx set in its Modelfile; Ollama will use its server default (4096 unless OLLAMA_CONTEXT_LENGTH is set). Model native max=%d", nativeMax),
	}
}

// ParseNumCtxParameter extracts `num_ctx <N>` from an Ollama /api/show
// `parameters` blob. Returns 0 if not present.
func ParseNumCtxParameter(s string) int {
	m := numCtxRe.FindStringSubmatch(s)
	if len(m) != 2 {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// parseNativeContext looks for any key ending in ".context_length" inside the
// /api/show `model_info` map and returns its numeric value. Keys look like
// "qwen2.context_length", "llama.context_length", etc.
func parseNativeContext(info map[string]any) int {
	for k, v := range info {
		if !strings.HasSuffix(k, ".context_length") {
			continue
		}
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case json.Number:
			i, _ := n.Int64()
			return int(i)
		}
	}
	return 0
}
