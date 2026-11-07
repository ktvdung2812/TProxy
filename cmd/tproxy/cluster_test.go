package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const testClusterStatus = `{"role":"member","node":{"id":"n1","name":"local"},"cluster":{"name":"Example","hub_url":"https://hub"},"link":{"state":"connected","rtt_ms":12},"lending":{"enabled":true,"models":["model"],"limits":{"requests_per_minute":20,"concurrent_streams":2}},"borrowing":{"enabled":true,"mode":"fallback"},"nodes":[{"name":"local","role":"member","status":"active","online":true,"self":true,"version":"1","offers":[{"eligible_accounts":1},{"eligible_accounts":0}]},{"name":"hub","role":"hub","status":"active","offers":[{"eligible_accounts":0}]}],"warnings":["caution"]}`

type clusterRequest struct {
	method, path string
	body         map[string]any
}

func clusterFake(t *testing.T, status string) (*httptest.Server, *[]clusterRequest) {
	t.Helper()
	var requests []clusterRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("authorization = %q", got)
		}
		var body map[string]any
		if r.Body != nil && r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
		}
		requests = append(requests, clusterRequest{r.Method, r.URL.Path, body})
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/admin/cluster/tokens" {
			fmt.Fprint(w, `{"token":"tpc1_test","id":"id","expires_at":"tomorrow","command":"join me"}`)
			return
		}
		fmt.Fprint(w, status)
	}))
	t.Cleanup(server.Close)
	return server, &requests
}
func invokeCluster(server *httptest.Server, input string, args ...string) (string, string, error) {
	var out, stderr bytes.Buffer
	args = append(args, "--url", server.URL, "--secret", "secret")
	err := runCluster(args, strings.NewReader(input), &out, &stderr)
	return out.String(), stderr.String(), err
}
func TestClusterRequests(t *testing.T) {
	lending := map[string]any{"enabled": true, "models": []any{}, "limits": map[string]any{"requests_per_minute": float64(20), "concurrent_streams": float64(2)}}
	disabled := map[string]any{"enabled": false, "models": []any{}, "limits": map[string]any{"requests_per_minute": float64(20), "concurrent_streams": float64(2)}}
	tests := []struct {
		name     string
		args     []string
		status   string
		requests []clusterRequest
		output   string
	}{
		{"status", []string{"status"}, testClusterStatus, []clusterRequest{{"GET", "/api/admin/cluster", nil}}, "connected"},
		{"create", []string{"create", "--name", "Example", "--hub-url", "https://hub", "--allow-insecure", "--no-lend"}, testClusterStatus, []clusterRequest{{"POST", "/api/admin/cluster/create", map[string]any{"name": "Example", "hub_url": "https://hub", "allow_insecure": true, "lending": disabled}}}, "tproxy cluster token"},
		{"token 36h", []string{"token", "--label", "label", "--ttl", "36h", "--uses", "3"}, testClusterStatus, []clusterRequest{{"POST", "/api/admin/cluster/tokens", map[string]any{"label": "label", "ttl_hours": float64(36), "max_uses": float64(3)}}}, "shown only once"},
		{"token 90m", []string{"token", "--ttl", "90m"}, testClusterStatus, []clusterRequest{{"POST", "/api/admin/cluster/tokens", map[string]any{"label": "", "ttl_hours": float64(2), "max_uses": float64(1)}}}, "join me"},
		{"join leading", []string{"join", "tpc1_test", "--name", "node", "--owner", "owner"}, testClusterStatus, []clusterRequest{{"POST", "/api/admin/cluster/join", map[string]any{"token": "tpc1_test", "node_name": "node", "owner": "owner", "allow_insecure": false, "lending": lending}}}, "connected"},
		{"leave", []string{"leave", "--yes"}, testClusterStatus, []clusterRequest{{"GET", "/api/admin/cluster", nil}, {"POST", "/api/admin/cluster/leave", nil}}, "Left"},
		{"dissolve", []string{"dissolve", "--yes"}, strings.Replace(testClusterStatus, `"role":"member"`, `"role":"hub"`, 1), []clusterRequest{{"GET", "/api/admin/cluster", nil}, {"POST", "/api/admin/cluster/dissolve", nil}}, "dissolved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, requests := clusterFake(t, tt.status)
			out, _, err := invokeCluster(server, "", tt.args...)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*requests, tt.requests) {
				t.Errorf("requests = %#v, want %#v", *requests, tt.requests)
			}
			if !strings.Contains(out, tt.output) {
				t.Errorf("output = %q", out)
			}
			if strings.Contains(out, "[y/N]") {
				t.Errorf("unexpected prompt: %q", out)
			}
		})
	}
	// Place the token after every flag, including the common flags.
	server, requests := clusterFake(t, testClusterStatus)
	var out, stderr bytes.Buffer
	err := runCluster([]string{"join", "--url", server.URL, "--secret", "secret", "--no-lend", "tpc1_test"}, strings.NewReader(""), &out, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 1 || (*requests)[0].body["token"] != "tpc1_test" || (*requests)[0].body["lending"].(map[string]any)["enabled"] != false {
		t.Fatalf("requests = %#v", *requests)
	}
}
func TestClusterStatusOutput(t *testing.T) {
	server, _ := clusterFake(t, testClusterStatus)
	out, _, err := invokeCluster(server, "", "status")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"member", "Example", "connected", "local (self)", "hub", "caution", "1/2", "0/1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	out, _, err = invokeCluster(server, "", "status", "--json")
	if err != nil || !json.Valid([]byte(out)) {
		t.Fatalf("JSON = %q, err = %v", out, err)
	}
}

func TestClusterStatusTableColumns(t *testing.T) {
	status := `{"nodes":[{"name":"192.168.1.81","role":"hub","status":"active","online":true,"self":true,"version":"0.1.25","offers":[{"eligible_accounts":1},{"eligible_accounts":2},{"eligible_accounts":0}]},{"name":"member-a","role":"member","status":"active","online":true,"version":"0.1.25","offers":[{"eligible_accounts":1},{"eligible_accounts":0},{"eligible_accounts":0}]},{"name":"member-b-with-a-very-long-node-name","role":"member","status":"active","online":true,"version":"0.1.25-a-very-long-version","offers":[{"eligible_accounts":0},{"eligible_accounts":0},{"eligible_accounts":0}]}]}`
	server, _ := clusterFake(t, status)
	out, _, err := invokeCluster(server, "", "status")
	if err != nil {
		t.Fatal(err)
	}
	_, table, ok := strings.Cut(out, "Nodes:\n")
	if !ok {
		t.Fatalf("missing nodes table in %q", out)
	}
	lines := strings.Split(table, "\n")
	want := [][]string{
		{"NAME", "ROLE", "STATUS", "ONLINE", "OFFERS", "VERSION"},
		{"192.168.1.81 (self)", "hub", "active", "online", "2/3", "0.1.25"},
		{"member-a", "member", "active", "online", "1/3", "0.1.25"},
		{"member-b-with-a-very-long-node-name", "member", "active", "online", "0/3", "0.1.25-a-very-long-version"},
	}
	if len(lines) < len(want) {
		t.Fatalf("table has %d lines, want at least %d: %q", len(lines), len(want), table)
	}
	for i, fields := range want {
		got := strings.Split(lines[i], "  ")
		var nonempty []string
		for _, field := range got {
			if field = strings.TrimSpace(field); field != "" {
				nonempty = append(nonempty, field)
			}
		}
		if !reflect.DeepEqual(nonempty, fields) {
			t.Errorf("row %d = %q, want %q", i, nonempty, fields)
		}
	}
}
func TestClusterConfirmations(t *testing.T) {
	hub := strings.Replace(testClusterStatus, `"role":"member"`, `"role":"hub"`, 1)
	server, requests := clusterFake(t, hub)
	_, _, err := invokeCluster(server, "", "leave", "--yes")
	if err == nil || !strings.Contains(err.Error(), "dissolve") || len(*requests) != 1 {
		t.Fatalf("err = %v, requests = %#v", err, *requests)
	}
	server2, requests2 := clusterFake(t, testClusterStatus)
	out, _, err := invokeCluster(server2, "n\n", "leave")
	if err != nil || !strings.Contains(out, "Leave the cluster Example? [y/N]") || len(*requests2) != 1 {
		t.Fatalf("out = %q, err = %v, requests = %#v", out, err, *requests2)
	}
	_, _, err = invokeCluster(server2, "", "dissolve", "--yes")
	if err == nil || !strings.Contains(err.Error(), "leave") {
		t.Fatalf("err = %v", err)
	}
	server3, requests3 := clusterFake(t, hub)
	out, _, err = invokeCluster(server3, "n\n", "dissolve")
	if err != nil || !strings.Contains(out, "member nodes, join tokens and the ledger") || len(*requests3) != 1 {
		t.Fatalf("out = %q, err = %v, requests = %#v", out, err, *requests3)
	}
}
func TestClusterErrorAndHelp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"code":"invalid_join_token","message":"token rejected"}}`)
	}))
	defer server.Close()
	_, _, err := invokeCluster(server, "", "status")
	if err == nil || err.Error() != "token rejected" {
		t.Fatalf("err = %v", err)
	}
	var out, stderr bytes.Buffer
	if err := runCluster([]string{"help"}, strings.NewReader(""), &out, &stderr); err != nil || out.String() != clusterUsage {
		t.Fatalf("help = %q, err = %v", out.String(), err)
	}
	if err := runCluster([]string{"unknown"}, strings.NewReader(""), &out, &stderr); err == nil || !strings.Contains(err.Error(), clusterUsage) {
		t.Fatalf("unknown err = %v", err)
	}
}
