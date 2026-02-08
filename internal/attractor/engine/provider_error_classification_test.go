package engine

import (
	"errors"
	"strings"
	"testing"
)

func TestClassifyProviderCLIError_AnthropicStreamJSONRequiresVerbose(t *testing.T) {
	got := classifyProviderCLIError(
		"anthropic",
		"error: --output-format stream-json requires --verbose",
		errors.New("exit status 2"),
	)

	if got.FailureClass != failureClassDeterministic {
		t.Fatalf("FailureClass: got %q want %q", got.FailureClass, failureClassDeterministic)
	}
	if !strings.HasPrefix(got.FailureSignature, "provider_contract|anthropic|") {
		t.Fatalf("FailureSignature: got %q", got.FailureSignature)
	}
}

func TestClassifyProviderCLIError_GeminiModelNotFound(t *testing.T) {
	got := classifyProviderCLIError(
		"google",
		"Error: model gemini-2.5-pro was not found",
		errors.New("exit status 1"),
	)

	if got.FailureClass != failureClassDeterministic {
		t.Fatalf("FailureClass: got %q want %q", got.FailureClass, failureClassDeterministic)
	}
	if !strings.HasPrefix(got.FailureSignature, "provider_model_unavailable|google|") {
		t.Fatalf("FailureSignature: got %q", got.FailureSignature)
	}
}

func TestClassifyProviderCLIError_CodexIdleTimeout_RunErrSignal(t *testing.T) {
	got := classifyProviderCLIError(
		"openai",
		"",
		errors.New("codex cli idle timeout after 2m0s with no output activity"),
	)

	if got.FailureClass != failureClassTransientInfra {
		t.Fatalf("FailureClass: got %q want %q", got.FailureClass, failureClassTransientInfra)
	}
	if !strings.HasPrefix(got.FailureSignature, "provider_timeout|openai|") {
		t.Fatalf("FailureSignature: got %q", got.FailureSignature)
	}
}

func TestClassifyProviderCLIError_UnknownFallbackIsDeterministic(t *testing.T) {
	got := classifyProviderCLIError(
		"anthropic",
		"fatal: unexpected failure",
		errors.New("exit status 1"),
	)

	if got.FailureClass != failureClassDeterministic {
		t.Fatalf("FailureClass: got %q want %q", got.FailureClass, failureClassDeterministic)
	}
	if !strings.HasPrefix(got.FailureSignature, "provider_failure|anthropic|unknown") {
		t.Fatalf("FailureSignature: got %q", got.FailureSignature)
	}
}
