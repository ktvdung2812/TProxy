package devin

import "testing"

func TestResolveChatModelUID(t *testing.T) {
	cases := []struct {
		model  string
		effort string
		want   string
	}{
		{"", "", "swe-2-high"},
		{"swe-1-6-fast", "", "swe-1-6-fast"}, // known suffix passes through
		{"claude-opus-5-high", "", "claude-opus-5-high"},
		{"devin/claude-opus-5-high", "", "claude-opus-5-high"}, // devin/ prefix
		{"claude-opus-5", "", "claude-opus-5-medium"},          // default effort = medium
		{"claude-opus-5", "max", "claude-opus-5-max"},
		{"claude-opus-5:high", "", "claude-opus-5-high"}, // colon suffix
		{"swe-2", "", "swe-2-high"},                      // swe-2 defaults high
		{"swe-1-6", "", "swe-1-6"},                       // bare base stays bare
		{"swe-1-6", "fast", "swe-1-6-fast"},
		{"swe-1-7", "", "swe-1-7"},
		{"swe-1-7", "medium", "swe-1-7-medium"},
		{"glm-5-2", "", "glm-5-2"},
		{"glm-5-2", "none", "glm-5-2-none"},
		{"glm-5-2", "max", "glm-5-2-max"},
		{"gpt-5-6-sol", "", "gpt-5-6-sol-low"}, // gpt-5 defaults low
		{"gpt-5-6-sol", "xhigh", "gpt-5-6-sol-xhigh"},
		{"gemini-3-8-flash", "", "gemini-3-8-flash-high"}, // gemini defaults high
		{"gemini-3-flash", "", "gemini-3-8-flash-high"},   // alias → 3.8
		{"claude-haiku-4-5", "", "MODEL_PRIVATE_11"},
		{"claude-sonnet-4-5", "", "MODEL_PRIVATE_2"},
		{"claude-sonnet-4-5", "high", "MODEL_PRIVATE_3"},
		{"some-unknown-model", "", "some-unknown-model"},       // no levels → bare
		{"gemini-3-8-flash", "xhigh", "gemini-3-8-flash-high"}, // clamps to nearest
		{"gpt-5-4", "adaptive", "gpt-5-4-high"},                // auto → high
	}
	for _, c := range cases {
		if got := ResolveChatModelUID(c.model, c.effort); got != c.want {
			t.Errorf("ResolveChatModelUID(%q, %q) = %q, want %q", c.model, c.effort, got, c.want)
		}
	}
}

func TestRegisterModelLevels(t *testing.T) {
	RegisterModelLevels([]string{"newmodel-alpha-low", "newmodel-alpha-high", "newmodel-alpha-max"})
	if got := ResolveChatModelUID("newmodel-alpha", "max"); got != "newmodel-alpha-max" {
		t.Fatalf("registered level resolve = %q", got)
	}
	if got := ResolveChatModelUID("newmodel-alpha", "xhigh"); got != "newmodel-alpha-max" {
		t.Fatalf("clamp to registered nearest = %q", got)
	}
}
