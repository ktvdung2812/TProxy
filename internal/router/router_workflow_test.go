package router

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

func TestWrapEventsRecordsCanceledStreamUsage(t *testing.T) {
	dataStore := workflowTestStore(t)
	requestRouter := New(dataStore, providers.NewRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	input := make(chan canonical.Event, 17)
	for index := 0; index < cap(input); index++ {
		input <- canonical.Event{Type: canonical.EventTextDelta, Text: "chunk"}
	}
	close(input)
	output := requestRouter.wrapEvents(ctx, workflowTestModel(), workflowTestSelection(), canonical.Request{RequestID: "canceled-stream"}, time.Now(), input, nil)
	deadline := time.Now().Add(time.Second)
	for len(output) < cap(output) && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if len(output) != cap(output) {
		t.Fatalf("wrapped stream buffer=%d want %d", len(output), cap(output))
	}
	cancel()
	for range output {
	}
	usage, err := dataStore.RecentUsage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 || usage[0].Status != 499 {
		t.Fatalf("canceled stream usage=%+v", usage)
	}
	if usage[0].ErrorCode != "client_canceled" {
		t.Fatalf("canceled stream error code=%q", usage[0].ErrorCode)
	}
}

func TestWrapEventsMarksEOFWithoutTerminalEventAsFailure(t *testing.T) {
	dataStore := workflowTestStore(t)
	requestRouter := New(dataStore, providers.NewRegistry())
	input := make(chan canonical.Event, 1)
	input <- canonical.Event{Type: canonical.EventTextDelta, Text: "partial"}
	close(input)
	output := requestRouter.wrapEvents(context.Background(), workflowTestModel(), workflowTestSelection(), canonical.Request{RequestID: "incomplete-stream"}, time.Now(), input, nil)
	foundError := false
	for event := range output {
		if event.Type == canonical.EventError {
			foundError = true
		}
	}
	if !foundError {
		t.Fatal("incomplete stream closed without an error event")
	}
	usage, err := dataStore.RecentUsage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 || usage[0].Status != 502 {
		t.Fatalf("incomplete stream usage=%+v", usage)
	}
	if usage[0].ErrorCode != "upstream_stream_incomplete" {
		t.Fatalf("incomplete stream error code=%q", usage[0].ErrorCode)
	}
}

func TestWrapEventsMergesUsageChunks(t *testing.T) {
	dataStore := workflowTestStore(t)
	requestRouter := New(dataStore, providers.NewRegistry())
	input := make(chan canonical.Event, 4)
	input <- canonical.Event{Type: canonical.EventUsage, Usage: &canonical.Usage{InputTokens: 11, CachedTokens: 3}}
	input <- canonical.Event{Type: canonical.EventUsage, Usage: &canonical.Usage{OutputTokens: 7, ReasoningTokens: 2}}
	input <- canonical.Event{Type: canonical.EventMessageEnd}
	close(input)
	output := requestRouter.wrapEvents(context.Background(), workflowTestModel(), workflowTestSelection(), canonical.Request{RequestID: "merged-usage"}, time.Now(), input, nil)
	for range output {
	}
	usage, err := dataStore.RecentUsage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 || usage[0].InputTokens != 11 || usage[0].OutputTokens != 7 || usage[0].ReasoningTokens != 2 {
		t.Fatalf("merged usage=%+v", usage)
	}
}

func TestWrapEventsFinalizesOnMessageEndWithoutWaitingForChannelClose(t *testing.T) {
	dataStore := workflowTestStore(t)
	requestRouter := New(dataStore, providers.NewRegistry())
	input := make(chan canonical.Event, 1)
	input <- canonical.Event{Type: canonical.EventMessageEnd}
	released := make(chan struct{})
	output := requestRouter.wrapEvents(context.Background(), workflowTestModel(), workflowTestSelection(), canonical.Request{RequestID: "terminal-open-channel"}, time.Now(), input, func() { close(released) })
	select {
	case event := <-output:
		if event.Type != canonical.EventMessageEnd {
			t.Fatalf("terminal event=%+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("message end was not forwarded")
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("stream release waited for an open adapter channel")
	}
	close(input)
	usage, err := dataStore.RecentUsage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 || usage[0].Status != 200 {
		t.Fatalf("terminal usage=%+v", usage)
	}
}

func TestWrapEventsFinalizesOnErrorWithoutWaitingForChannelClose(t *testing.T) {
	dataStore := workflowTestStore(t)
	requestRouter := New(dataStore, providers.NewRegistry())
	input := make(chan canonical.Event, 1)
	input <- canonical.Event{Type: canonical.EventError, Err: &providers.ProviderError{Status: 502, Code: "upstream_broken", Message: "broken"}}
	released := make(chan struct{})
	output := requestRouter.wrapEvents(context.Background(), workflowTestModel(), workflowTestSelection(), canonical.Request{RequestID: "error-open-channel"}, time.Now(), input, func() { close(released) })
	select {
	case event := <-output:
		if event.Type != canonical.EventError || event.Err == nil || event.Err.Error() != "broken" {
			t.Fatalf("error event=%+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("error event was not forwarded")
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("error stream release waited for an open adapter channel")
	}
	close(input)
	usage, err := dataStore.RecentUsage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 || usage[0].Status != 502 || usage[0].ErrorCode != "upstream_broken" {
		t.Fatalf("error usage=%+v", usage)
	}
}

func TestValidateStreamResultRejectsNilChannel(t *testing.T) {
	if err := validateStreamResult(nil, nil); providers.Code(err) != "provider_protocol_error" {
		t.Fatalf("nil stream validation error=%v code=%q", err, providers.Code(err))
	}
	events := make(chan canonical.Event)
	close(events)
	if err := validateStreamResult(events, nil); err != nil {
		t.Fatalf("valid stream rejected: %v", err)
	}
}

func workflowTestModel() store.PublicModel {
	return store.PublicModel{ID: "workflow-model"}
}

func workflowTestSelection() Selection {
	return Selection{
		Model:      workflowTestModel(),
		Route:      store.RouteTarget{UpstreamModel: "workflow-upstream"},
		Provider:   store.Provider{ID: "workflow-provider"},
		Credential: store.Credential{ID: "workflow-credential"},
		Attempt:    1,
	}
}

func workflowTestStore(t *testing.T) *store.Store {
	t.Helper()
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := store.OpenSQLite(filepath.Join(t.TempDir(), "workflow.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	return dataStore
}
