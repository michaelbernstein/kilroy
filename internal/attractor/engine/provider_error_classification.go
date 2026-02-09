package engine

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/strongdm/kilroy/internal/providerspec"
)

type providerCLIClassifiedError struct {
	FailureClass     string
	FailureSignature string
	FailureReason    string
}

type providerCLIErrorKind string

const (
	providerCLIErrorKindUnknown           providerCLIErrorKind = "unknown"
	providerCLIErrorKindExecutableMissing providerCLIErrorKind = "executable_missing"
	providerCLIErrorKindCapabilityMissing providerCLIErrorKind = "capability_missing"
)

type providerCLIContractError struct {
	Kind    providerCLIErrorKind
	Message string
}

func classifyProviderCLIError(provider string, stderr string, runErr error) providerCLIClassifiedError {
	providerKey := normalizeProviderKey(provider)
	if providerKey == "" {
		providerKey = "unknown"
	}

	runErrText := ""
	if runErr != nil {
		runErrText = strings.TrimSpace(runErr.Error())
	}
	stderrText := strings.TrimSpace(stderr)
	combined := strings.ToLower(strings.TrimSpace(runErrText + "\n" + stderrText))

	reason := strings.TrimSpace(runErrText)
	if reason == "" {
		reason = firstNonEmptyLine(stderrText)
	}
	if reason == "" {
		reason = "provider cli invocation failed"
	}

	contract := classifyProviderCLIErrorWithContract(providerKey, defaultCLISpecForProvider(providerKey), stderrText, runErr)
	switch contract.Kind {
	case providerCLIErrorKindExecutableMissing:
		return providerCLIClassifiedError{
			FailureClass:     failureClassDeterministic,
			FailureSignature: fmt.Sprintf("provider_executable_missing|%s|not_found", providerKey),
			FailureReason:    contract.Message,
		}
	case providerCLIErrorKindCapabilityMissing:
		return providerCLIClassifiedError{
			FailureClass:     failureClassDeterministic,
			FailureSignature: fmt.Sprintf("provider_contract|%s|capability_missing", providerKey),
			FailureReason:    contract.Message,
		}
	}

	if providerKey == "anthropic" &&
		strings.Contains(combined, "stream-json") &&
		strings.Contains(combined, "verbose") {
		return providerCLIClassifiedError{
			FailureClass:     failureClassDeterministic,
			FailureSignature: "provider_contract|anthropic|stream_json_requires_verbose",
			FailureReason:    "anthropic stream-json contract requires --verbose",
		}
	}

	if providerKey == "google" && isGoogleModelNotFound(combined) {
		return providerCLIClassifiedError{
			FailureClass:     failureClassDeterministic,
			FailureSignature: "provider_model_unavailable|google|model_not_found",
			FailureReason:    reason,
		}
	}

	if strings.Contains(combined, "idle timeout") || strings.Contains(combined, "timed out") {
		return providerCLIClassifiedError{
			FailureClass:     failureClassTransientInfra,
			FailureSignature: fmt.Sprintf("provider_timeout|%s|timeout", providerKey),
			FailureReason:    reason,
		}
	}
	if strings.Contains(combined, "rate limit") || strings.Contains(combined, "too many requests") {
		return providerCLIClassifiedError{
			FailureClass:     failureClassTransientInfra,
			FailureSignature: fmt.Sprintf("provider_rate_limit|%s|rate_limited", providerKey),
			FailureReason:    reason,
		}
	}
	if strings.Contains(combined, "connection refused") ||
		strings.Contains(combined, "connection reset") ||
		strings.Contains(combined, "broken pipe") ||
		strings.Contains(combined, "temporary failure") ||
		strings.Contains(combined, "service unavailable") ||
		strings.Contains(combined, "gateway timeout") {
		return providerCLIClassifiedError{
			FailureClass:     failureClassTransientInfra,
			FailureSignature: fmt.Sprintf("provider_transport|%s|network_unavailable", providerKey),
			FailureReason:    reason,
		}
	}

	return providerCLIClassifiedError{
		FailureClass:     failureClassDeterministic,
		FailureSignature: fmt.Sprintf("provider_failure|%s|unknown", providerKey),
		FailureReason:    reason,
	}
}

func classifyProviderCLIErrorWithContract(provider string, spec *providerspec.CLISpec, stderr string, runErr error) providerCLIContractError {
	if isExecutableNotFound(runErr) {
		return providerCLIContractError{
			Kind:    providerCLIErrorKindExecutableMissing,
			Message: "provider executable not found",
		}
	}
	if spec != nil && !probeOutputLooksLikeHelpFromSpec(spec, stderr) && strings.Contains(strings.ToLower(stderr), "unknown option") {
		return providerCLIContractError{
			Kind:    providerCLIErrorKindCapabilityMissing,
			Message: "provider CLI missing required capability flags",
		}
	}
	return providerCLIContractError{Kind: providerCLIErrorKindUnknown}
}

func isExecutableNotFound(runErr error) bool {
	if runErr == nil {
		return false
	}
	if _, ok := runErr.(*exec.Error); ok {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(runErr.Error()))
	return strings.Contains(text, "executable file not found") || strings.Contains(text, "no such file or directory")
}

func isGoogleModelNotFound(s string) bool {
	if !strings.Contains(s, "model") {
		return false
	}
	if strings.Contains(s, "not found") {
		return true
	}
	if strings.Contains(s, "unknown model") {
		return true
	}
	if strings.Contains(s, "does not exist") {
		return true
	}
	return false
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}
