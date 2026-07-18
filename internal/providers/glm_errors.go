package providers

import "strings"

func enrichGLMUpstreamMessage(baseURL, message string, status int) string {
	if status != 400 {
		return message
	}
	lower := strings.ToLower(message)
	if !strings.Contains(lower, "invalid api parameter") {
		return message
	}
	base := strings.ToLower(strings.TrimSpace(baseURL))
	if !strings.Contains(base, "api.z.ai") && !strings.Contains(base, "bigmodel.cn") {
		return message
	}
	hint := "Z.AI hint: GLM Coding Plan keys need base URL https://api.z.ai/api/coding/paas/v4; pay-as-you-go keys use https://api.z.ai/api/paas/v4. Non-vision models (glm-5.2) reject image attachments."
	if strings.Contains(base, "/api/coding/paas/v4") {
		hint = "Z.AI hint: this looks like a Coding Plan endpoint. If your key is pay-as-you-go, switch base URL to https://api.z.ai/api/paas/v4. Non-vision models reject image attachments."
	} else if strings.Contains(base, "/api/paas/v4") {
		hint = "Z.AI hint: this looks like a General API endpoint. If your key is from GLM Coding Plan, switch base URL to https://api.z.ai/api/coding/paas/v4. Non-vision models reject image attachments."
	}
	return strings.TrimSpace(message + " " + hint)
}

func alternateGLMBaseURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch {
	case strings.HasSuffix(base, "/api/coding/paas/v4"):
		return strings.TrimSuffix(base, "/api/coding/paas/v4") + "/api/paas/v4"
	case strings.HasSuffix(base, "/api/paas/v4"):
		return strings.TrimSuffix(base, "/api/paas/v4") + "/api/coding/paas/v4"
	default:
		return ""
	}
}
