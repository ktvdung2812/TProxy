package cursor

import "testing"

func TestEncodeAvailableModelsRequest(t *testing.T) {
	got := EncodeAvailableModelsRequest()
	want := []byte{0x28, 0x01, 0x38, 0x01}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d = %#x, want %#x", i, got[i], want[i])
		}
	}
}

func TestModelsFromParameterizedMetadataUsesVariantString(t *testing.T) {
	entries := modelsFromParameterizedMetadata([]parameterizedModel{
		{
			Name:              "claude-opus-4-8",
			ClientDisplayName: "Claude Opus 4.8",
			SupportsImages:    true,
			Variants: []parameterizedVariant{
				{
					VariantStringRepresentation: "claude-opus-4-8-thinking-high",
					DisplayNameOutsidePicker:    "Opus 4.8 1M Thinking High",
					Parameters: []modelParameter{
						{ID: "reasoning", Value: "high"},
						{ID: "context", Value: "1m"},
					},
				},
			},
		},
	})
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].ID != "claude-opus-4-8-thinking-high" {
		t.Fatalf("id = %q", entries[0].ID)
	}
	if entries[0].Name != "Opus 4.8 1M Thinking High" {
		t.Fatalf("name = %q", entries[0].Name)
	}
	if !entries[0].SupportsImages {
		t.Fatalf("expected vision capability")
	}
}

func TestModelsFromParameterizedMetadataBuildsEffortRows(t *testing.T) {
	entries := modelsFromParameterizedMetadata([]parameterizedModel{
		{
			Name:              "cursor-grok-4.5",
			ClientDisplayName: "Cursor Grok 4.5",
			Variants: []parameterizedVariant{
				{
					Parameters:  []modelParameter{{ID: "effort", Value: "low"}},
					DisplayName: "Low",
				},
				{
					Parameters:  []modelParameter{{ID: "effort", Value: "high"}},
					DisplayName: "High",
				},
			},
		},
	})
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	ids := map[string]bool{entries[0].ID: true, entries[1].ID: true}
	if !ids["cursor-grok-4.5-low"] || !ids["cursor-grok-4.5-high"] {
		t.Fatalf("ids = %+v", ids)
	}
}

func TestDecodeAvailableModelsResponse(t *testing.T) {
	payload := EncodeAvailableModelsRequest()
	frame := wrapConnectRPCFrame(append([]byte{}, payload...), false)
	// Build a minimal response with one model entry.
	modelBytes := []byte{
		0x0a, 0x07, 'd', 'e', 'f', 'a', 'u', 'l', 't',
	}
	responsePayload := append([]byte{0x12}, byte(len(modelBytes)))
	responsePayload = append(responsePayload, modelBytes...)
	response := wrapConnectRPCFrame(responsePayload, false)

	// Sanity: request frame decodes.
	if got := ParseConnectRPCFrame(frame); got == nil {
		t.Fatal("request frame parse failed")
	}

	models, err := decodeAvailableModelsConnectBody(response)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Name != "default" {
		t.Fatalf("models = %+v", models)
	}
}
