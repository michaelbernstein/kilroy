package providerspec

import "testing"

func TestBuiltinSpecsIncludeCoreAndNewProviders(t *testing.T) {
	s := Builtins()
	for _, key := range []string{"openai", "anthropic", "google", "kimi", "zai"} {
		if _, ok := s[key]; !ok {
			t.Fatalf("missing builtin provider %q", key)
		}
	}
}

func TestCanonicalProviderKey_Aliases(t *testing.T) {
	if got := CanonicalProviderKey("gemini"); got != "google" {
		t.Fatalf("gemini alias: got %q want %q", got, "google")
	}
	if got := CanonicalProviderKey(" Z-AI "); got != "zai" {
		t.Fatalf("z-ai alias: got %q want %q", got, "zai")
	}
	if got := CanonicalProviderKey("moonshot"); got != "kimi" {
		t.Fatalf("moonshot alias: got %q want %q", got, "kimi")
	}
	if got := CanonicalProviderKey("glm"); got != "glm" {
		t.Fatalf("unknown provider keys should pass through unchanged, got %q", got)
	}
}
