package server

import "strings"

func canonicalProviderID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	id = strings.ReplaceAll(id, "-", "_")
	id = strings.ReplaceAll(id, " ", "_")
	return id
}

// isSpendOrAPIProvider is CodexBar/OpenUsage noise: org spend dashboards and
// API-key charts, not a coding-plan quota the phone should track.
func isSpendOrAPIProvider(id string) bool {
	switch canonicalProviderID(id) {
	case "openai", "openai_api", "open_ai",
		"anthropic", "anthropic_api",
		"openrouter", "litellm", "llm_proxy", "sub2api", "clawrouter",
		"groq", "groqcloud", "elevenlabs", "deepgram",
		"bedrock", "aws_bedrock", "vertex", "vertex_ai",
		"ollama", "mistral", "venice", "synthetic",
		"deepseek", "moonshot", "deepinfra", "doubao",
		"poe", "perplexity", "t3chat", "manus", "qoder":
		return true
	default:
		return false
	}
}

func isCodingPlanProvider(id string) bool {
	switch canonicalProviderID(id) {
	case "cursor",
		"codex", "codex_cli",
		"claude", "claude_code",
		"copilot", "github_copilot",
		"gemini", "gemini_cli",
		"grok", "xai",
		"kilo", "warp", "zed", "windsurf", "augment", "kiro", "codebuff":
		return true
	default:
		return false
	}
}

func isAPINoiseWindow(providerID, key, title string) bool {
	blob := strings.ToLower(strings.TrimSpace(key + " " + title))
	if blob == "" {
		return false
	}
	if strings.Contains(blob, "third party") || strings.Contains(blob, "third-party") {
		return true
	}
	if canonicalProviderID(providerID) == "cursor" && key == "tertiary" {
		return true
	}
	if strings.Contains(blob, "plan_api") || blob == "api" || strings.HasPrefix(blob, "api ") || strings.Contains(blob, " api") {
		return true
	}
	return false
}

func keepPlanProvider(p Provider) bool {
	if isSpendOrAPIProvider(p.ID) {
		return false
	}
	if len(p.Windows) > 0 {
		return true
	}
	if p.Error != "" || p.Stale {
		return isCodingPlanProvider(p.ID)
	}
	return false
}

func planWindowTitle(providerID, key, title string, minutes *float64) string {
	if canonicalProviderID(providerID) == "cursor" {
		switch key {
		case "primary", "plan_percent_used", "plan-percent-used":
			return "Plan"
		case "secondary", "plan_auto_percent_used", "plan-auto-percent-used":
			return "Auto"
		}
	}
	if title != "" {
		return title
	}
	return windowTitle(key, minutes)
}
