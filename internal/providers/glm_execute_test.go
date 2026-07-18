package providers

import "testing"

func TestGLMCapabilitiesForModel(t *testing.T) {
	if caps := glmCapabilitiesForModel("glm-5.2"); contains(caps, "vision") {
		t.Fatalf("glm-5.2 should not advertise vision: %#v", caps)
	}
	if caps := glmCapabilitiesForModel("glm-4.6v"); !contains(caps, "vision") {
		t.Fatalf("glm-4.6v should advertise vision: %#v", caps)
	}
}

func TestIsGLMParameterError(t *testing.T) {
	if !isGLMParameterError([]byte(`{"error":{"code":"1210","message":"Invalid API parameter"}}`), 400) {
		t.Fatal("expected glm parameter error")
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
