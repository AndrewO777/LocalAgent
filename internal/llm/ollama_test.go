package llm

import "testing"

func TestParseNumCtxParameter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"absent", "stop \"<|im_end|>\"\ntemperature 0.7", 0},
		{"simple", "num_ctx 32768", 32768},
		{"with leading whitespace", "  num_ctx  65536\nstop \"<eos>\"", 65536},
		{"multiline", "stop \"<eos>\"\nnum_ctx 4096\ntemperature 0.5", 4096},
		{"zero rejected", "num_ctx 0", 0},
		{"negative rejected", "num_ctx -1", 0},
		{"non-numeric rejected", "num_ctx abc", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseNumCtxParameter(tc.in); got != tc.want {
				t.Fatalf("ParseNumCtxParameter(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseNativeContext(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want int
	}{
		{"qwen2 float", map[string]any{"qwen2.context_length": float64(32768)}, 32768},
		{"llama int", map[string]any{"llama.context_length": 8192}, 8192},
		{"absent", map[string]any{"general.architecture": "qwen2"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseNativeContext(tc.in); got != tc.want {
				t.Fatalf("parseNativeContext = %d, want %d", got, tc.want)
			}
		})
	}
}
