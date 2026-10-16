package api

import (
	"fmt"
	"net/http"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/mcp"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/store"
)

func mcpBridgeHandler(server *Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, _ := r.Context().Value(apiKeyContext).(*store.APIKey)
		mcp.Handler(func(prompt string) (content string, err error) {
			defer func() {
				if err != nil {
					status := providers.Status(err)
					if status < 400 {
						status = http.StatusBadGateway
					}
					err = &mcp.RequestError{Err: err, Status: status, Code: providers.Code(err), RetryAfter: providers.RetryAfter(err)}
				}
			}()
			model, err := server.router.Resolve(r.Context(), "auto", key)
			if err != nil {
				return "", fmt.Errorf("resolve model: %w", err)
			}
			request := canonical.Request{
				RequestID: useClientRequestID(r),
				Source:    canonical.ProtocolOpenAI,
				Messages:  []canonical.Message{{Role: "user", Content: prompt}},
			}
			if err := server.prepareClientRequest(r, &request, key); err != nil {
				return "", err
			}
			result, err := server.router.Execute(r.Context(), *model, request)
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
		}).ServeHTTP(w, r)
	})
}
