package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

type elevenLabsAdapter struct{ client *http.Client }

func (a *elevenLabsAdapter) Execute(context.Context, store.Provider, store.Credential, canonical.Request) (*canonical.Response, error) {
	return nil, &ProviderError{Status: http.StatusBadRequest, Code: "capability_not_supported", Message: "ElevenLabs supports audio endpoints only"}
}

func (a *elevenLabsAdapter) ExecuteStream(context.Context, store.Provider, store.Credential, canonical.Request) (<-chan canonical.Event, error) {
	return nil, &ProviderError{Status: http.StatusBadRequest, Code: "capability_not_supported", Message: "ElevenLabs gateway streaming is not supported"}
}

func elevenLabsHeaders(provider store.Provider, credential store.Credential, rawRequest RawRequest) http.Header {
	headers := make(http.Header)
	for key, value := range provider.Headers {
		headers.Set(key, value)
	}
	if credential.Secret != "" && headers.Get("xi-api-key") == "" {
		headers.Set("xi-api-key", credential.Secret)
	}
	for _, name := range []string{"X-Request-ID", "Idempotency-Key"} {
		if value := rawRequest.Headers.Get(name); value != "" {
			headers.Set(name, value)
		}
	}
	return headers
}

func (a *elevenLabsAdapter) Proxy(ctx context.Context, provider store.Provider, credential store.Credential, rawRequest RawRequest) (*RawResponse, error) {
	ctx = withCredentialProxy(ctx, credential)
	switch rawRequest.Path {
	case "/v1/audio/speech":
		return a.speech(ctx, provider, credential, rawRequest)
	case "/v1/audio/transcriptions":
		return a.transcription(ctx, provider, credential, rawRequest)
	case "/v1/audio/voices":
		return a.voices(ctx, provider, credential, rawRequest)
	default:
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "capability_not_supported", Message: "ElevenLabs supports speech, transcription and voice catalog endpoints only"}
	}
}

func (a *elevenLabsAdapter) voices(ctx context.Context, provider store.Provider, credential store.Credential, rawRequest RawRequest) (*RawResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint(provider.BaseURL, "/v1/voices"), nil)
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	request.Header = elevenLabsHeaders(provider, credential, rawRequest)
	request.Header.Set("Accept", "application/json")
	response, err := a.doRaw(request)
	if err != nil {
		return response, err
	}
	var upstream map[string]any
	if json.Unmarshal(response.Body, &upstream) == nil {
		voices, _ := upstream["voices"].([]any)
		data := make([]any, 0, len(voices))
		for _, raw := range voices {
			voice, _ := raw.(map[string]any)
			data = append(data, map[string]any{"id": stringValue(firstValue(voice, "voice_id", "id")), "name": stringValue(voice["name"]), "category": voice["category"], "labels": voice["labels"]})
		}
		response.Body, _ = json.Marshal(map[string]any{"object": "list", "data": data})
		response.ContentType = "application/json"
	}
	return response, nil
}

func (a *elevenLabsAdapter) speech(ctx context.Context, provider store.Provider, credential store.Credential, rawRequest RawRequest) (*RawResponse, error) {
	var input map[string]any
	if err := json.Unmarshal(rawRequest.Body, &input); err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "speech request must be JSON", Err: err}
	}
	text := strings.TrimSpace(stringValue(input["input"]))
	voice := strings.TrimSpace(stringValue(firstValue(input, "voice", "voice_id")))
	model := strings.TrimSpace(stringValue(input["model"]))
	if text == "" || voice == "" || model == "" {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "input, voice and model are required"}
	}
	payload := map[string]any{"text": text, "model_id": model}
	if settings, ok := input["voice_settings"].(map[string]any); ok {
		payload["voice_settings"] = settings
	}
	if speed := input["speed"]; speed != nil {
		settings, _ := payload["voice_settings"].(map[string]any)
		if settings == nil {
			settings = map[string]any{}
		}
		settings["speed"] = speed
		payload["voice_settings"] = settings
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Err: err}
	}
	target := endpoint(provider.BaseURL, "/v1/text-to-speech/") + url.PathEscape(voice)
	if outputFormat := elevenLabsOutputFormat(stringValue(input["response_format"])); outputFormat != "" {
		target += "?output_format=" + url.QueryEscape(outputFormat)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(encoded))
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	request.Header = elevenLabsHeaders(provider, credential, rawRequest)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "audio/*")
	return a.doRaw(request)
}

func (a *elevenLabsAdapter) transcription(ctx context.Context, provider store.Provider, credential store.Credential, rawRequest RawRequest) (*RawResponse, error) {
	mediaType, parameters, err := mime.ParseMediaType(rawRequest.ContentType)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") || parameters["boundary"] == "" {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "transcription request must be multipart/form-data"}
	}
	body, contentType, model, err := elevenLabsTranscriptionBody(rawRequest.Body, parameters["boundary"])
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Err: err}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(provider.BaseURL, "/v1/speech-to-text"), bytes.NewReader(body))
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	request.Header = elevenLabsHeaders(provider, credential, rawRequest)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	response, err := a.doRaw(request)
	if err != nil {
		return response, err
	}
	var output map[string]any
	if json.Unmarshal(response.Body, &output) == nil {
		output["model"] = model
		output["object"] = "audio.transcription"
		response.Body, _ = json.Marshal(output)
		response.ContentType = "application/json"
	}
	return response, nil
}

func (a *elevenLabsAdapter) doRaw(request *http.Request) (*RawResponse, error) {
	response, err := a.client.Do(request)
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if readErr != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_read_error", Err: readErr}
	}
	result := &RawResponse{Status: response.StatusCode, Headers: response.Header.Clone(), Body: data, ContentType: response.Header.Get("Content-Type")}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, upstreamResponseError(response, data)
	}
	return result, nil
}

func elevenLabsTranscriptionBody(input []byte, boundary string) ([]byte, string, string, error) {
	reader := multipart.NewReader(bytes.NewReader(input), boundary)
	var output bytes.Buffer
	writer := multipart.NewWriter(&output)
	model := ""
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", "", err
		}
		data, err := io.ReadAll(io.LimitReader(part, 64<<20))
		if err != nil {
			return nil, "", "", err
		}
		if part.FormName() == "model" {
			model = strings.TrimSpace(string(data))
			field, createErr := writer.CreateFormField("model_id")
			if createErr != nil {
				return nil, "", "", createErr
			}
			_, _ = field.Write(data)
			continue
		}
		header := cloneMIMEHeader(part.Header)
		target, createErr := writer.CreatePart(header)
		if createErr != nil {
			return nil, "", "", createErr
		}
		if _, err = target.Write(data); err != nil {
			return nil, "", "", err
		}
	}
	if model == "" {
		return nil, "", "", fmt.Errorf("model field is required")
	}
	if err := writer.Close(); err != nil {
		return nil, "", "", err
	}
	return output.Bytes(), writer.FormDataContentType(), model, nil
}

func cloneMIMEHeader(source textproto.MIMEHeader) textproto.MIMEHeader {
	result := make(textproto.MIMEHeader, len(source))
	for key, values := range source {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func elevenLabsOutputFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "mp3":
		return "mp3_44100_128"
	case "opus":
		return "opus_48000_128"
	case "pcm", "wav":
		return "pcm_44100"
	default:
		return format
	}
}

var _ Adapter = (*elevenLabsAdapter)(nil)
var _ RawProxyAdapter = (*elevenLabsAdapter)(nil)
