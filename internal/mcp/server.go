package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Handler exposes a minimal MCP-compatible JSON-RPC surface for agent integrations.
func Handler(chat func(prompt string) (string, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeRPCError(w, nil, -32600, "POST required")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeRPCError(w, nil, -32700, "invalid request body")
			return
		}
		var envelope struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      any             `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			writeRPCError(w, nil, -32700, "parse error")
			return
		}
		switch envelope.Method {
		case "initialize":
			writeRPCResult(w, envelope.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]string{"name": "tproxy", "version": "1.0.0"},
				"capabilities":    map[string]any{"tools": map[string]any{}},
			})
		case "tools/list":
			writeRPCResult(w, envelope.ID, map[string]any{
				"tools": []map[string]any{
					{
						"name":        "tproxy_chat",
						"description": "Send a prompt through the tproxy gateway",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"prompt": map[string]string{"type": "string"},
							},
							"required": []string{"prompt"},
						},
					},
				},
			})
		case "tools/call":
			var params struct {
				Name      string `json:"name"`
				Arguments struct {
					Prompt string `json:"prompt"`
				} `json:"arguments"`
			}
			_ = json.Unmarshal(envelope.Params, &params)
			if params.Name != "tproxy_chat" {
				writeRPCError(w, envelope.ID, -32601, "unknown tool")
				return
			}
			prompt := strings.TrimSpace(params.Arguments.Prompt)
			if prompt == "" {
				writeRPCError(w, envelope.ID, -32602, "prompt required")
				return
			}
			if chat == nil {
				writeRPCError(w, envelope.ID, -32000, "chat bridge unavailable")
				return
			}
			content, err := chat(prompt)
			if err != nil {
				writeRPCError(w, envelope.ID, -32000, err.Error())
				return
			}
			writeRPCResult(w, envelope.ID, map[string]any{
				"content": []map[string]string{{"type": "text", "text": content}},
			})
		default:
			writeRPCError(w, envelope.ID, -32601, "method not found")
		}
	})
}

func writeRPCResult(w http.ResponseWriter, id any, result any) {
	writeJSON(w, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeRPCError(w http.ResponseWriter, id any, code int, message string) {
	writeJSON(w, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	encoded, _ := json.Marshal(payload)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}
