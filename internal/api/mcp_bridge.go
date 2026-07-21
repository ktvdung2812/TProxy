package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/mcp"
)

func mcpBridgeHandler(server *Server) http.Handler {
	return mcp.Handler(func(prompt string) (string, error) {
		model, err := server.router.Resolve(context.Background(), "auto", nil)
		if err != nil {
			return "", fmt.Errorf("resolve model: %w", err)
		}
		result, err := server.router.Execute(context.Background(), *model, canonical.Request{
			Source:   canonical.ProtocolOpenAI,
			Messages: []canonical.Message{{Role: "user", Content: prompt}},
		})
		if err != nil {
			return "", err
		}
		if result == nil || result.Response == nil {
			return "", fmt.Errorf("empty response")
		}
		switch value := result.Response.Content.(type) {
		case string:
			return value, nil
		default:
			return fmt.Sprint(value), nil
		}
	})
}
