package cluster

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/security"
)

const (
	hubCallTimeout       = 15 * time.Second
	joinTimeout          = 20 * time.Second
	leaveTimeout         = 5 * time.Second
	inventoryTimeout     = 10 * time.Second
	notifyTimeout        = 5 * time.Second
	linkHandshakeTimeout = 15 * time.Second
	syncTimeout          = 30 * time.Second
	storeTimeout         = 10 * time.Second
	unreachableAfter     = 3
	maxTextLength        = 100
	maxLedgerRows        = 10000
	defaultLedgerDays    = 7
	maxLedgerDays        = 31
	defaultClusterName   = "tproxy cluster"
	dayLayout            = "2006-01-02"
)

type errorEnvelope struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func errorBody(code, message string) []byte {
	data, _ := json.Marshal(errorEnvelope{Error: errorDetail{Type: "cluster_error", Code: code, Message: security.RedactText(message)}})
	return data
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-TProxy-Error-Code", code)
	w.WriteHeader(status)
	_, _ = w.Write(errorBody(code, message))
}

// syntheticResponse answers Do without reaching another node, in the shape
// the hub uses for its own errors.
func syntheticResponse(req *http.Request, status int, code, message string) *http.Response {
	body := errorBody(code, message)
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("X-TProxy-Error-Code", code)
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// readHubError extracts the cluster error code and message from a non-2xx
// hub response and consumes its body.
func readHubError(resp *http.Response) (code, message string) {
	code = resp.Header.Get("X-TProxy-Error-Code")
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var envelope errorEnvelope
	if json.Unmarshal(data, &envelope) == nil {
		if code == "" {
			code = envelope.Error.Code
		}
		message = envelope.Error.Message
	}
	if message == "" {
		message = strings.TrimSpace(string(data))
		if len(message) > 200 {
			message = message[:200]
		}
	}
	if message == "" {
		message = resp.Status
	}
	return code, security.RedactText(message)
}

func decodeBody(body io.Reader, limit int64, value any) error {
	return json.NewDecoder(io.LimitReader(body, limit)).Decode(value)
}

func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
	_ = body.Close()
}

func closeRequestBody(req *http.Request) {
	if req != nil && req.Body != nil {
		_ = req.Body.Close()
	}
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, method+" required")
	return false
}

func jsonHash(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func catalogVersion(c Catalog) string {
	c.Version = ""
	return jsonHash(c)
}

// stateVersion leaves out the fields that change on every poll, so an
// unchanged cluster can answer If-None-Match with 304.
func stateVersion(st State) string {
	st.Version = ""
	st.GeneratedAt = time.Time{}
	st.Nodes = stableNodes(st.Nodes)
	return jsonHash(st)
}

func peersKey(peers []NodeState) string {
	return jsonHash(stableNodes(peers))
}

func stableNodes(nodes []NodeState) []NodeState {
	out := make([]NodeState, len(nodes))
	for i, node := range nodes {
		node.RTTMS = 0
		node.LastSeenAt = time.Time{}
		out[i] = node
	}
	return out
}

// revokedResponse reports whether the hub itself refused the node token. A
// bare 401 or 403 can come from a proxy or WAF in front of the hub, and that
// must not end a membership.
func revokedResponse(status int, header http.Header) bool {
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		return false
	}
	code := header.Get("X-TProxy-Error-Code")
	return code == CodeUnauthorized || code == CodeNodeRevoked
}

func quoteETag(version string) string { return `"` + version + `"` }

func etagMatch(header, version string) bool {
	if version == "" {
		return false
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimPrefix(strings.TrimSpace(part), "W/")
		if part == "*" || strings.Trim(part, `"`) == version {
			return true
		}
	}
	return false
}

func sinceDay(now time.Time, days int) string {
	return now.UTC().AddDate(0, 0, -(days - 1)).Format(dayLayout)
}

func clampDays(days int) int {
	if days <= 0 {
		return defaultLedgerDays
	}
	return min(days, maxLedgerDays)
}

func parseDays(raw string) int {
	days, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return defaultLedgerDays
	}
	return clampDays(days)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func cleanText(value string) (string, bool) {
	value = strings.TrimSpace(value)
	return value, utf8.RuneCountInString(value) <= maxTextLength && !strings.ContainsFunc(value, unicode.IsControl)
}

// validNodeID keeps node ids usable as URL path segments and link hostnames.
func validNodeID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func linkHost(nodeID string) string { return nodeID + ".cluster" }

func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

// forwardHeaders copies request headers for another node. It drops hop-by-hop
// headers, credentials and every cluster header except the model hint, which
// the lender's policy reads.
func forwardHeaders(in http.Header) http.Header {
	out := in.Clone()
	if out == nil {
		out = http.Header{}
	}
	for _, value := range out.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				out.Del(name)
			}
		}
	}
	for _, name := range hopHeaders {
		out.Del(name)
	}
	out.Del("Authorization")
	out.Del("Host")
	stripClusterHeaders(out, HeaderModel)
	return out
}

func stripClusterHeaders(header http.Header, keep ...string) {
	for name := range header {
		canonical := http.CanonicalHeaderKey(name)
		if !strings.HasPrefix(canonical, "X-Tproxy-Cluster-") {
			continue
		}
		if slices.ContainsFunc(keep, func(k string) bool { return http.CanonicalHeaderKey(k) == canonical }) {
			continue
		}
		delete(header, name)
	}
}

// relayPath turns the escaped tail of a relay URL into the local path. Dot
// segments are refused so a relayed request cannot climb out of the gateway
// routes it was meant for.
func relayPath(restEscaped string) (path, rawPath string, ok bool) {
	unescaped, err := url.PathUnescape(restEscaped)
	if err != nil {
		return "", "", false
	}
	for _, segment := range strings.Split(unescaped, "/") {
		if segment == "." || segment == ".." {
			return "", "", false
		}
	}
	return "/" + unescaped, "/" + restEscaped, true
}

func normalizeList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func normalizeLending(p LendingPolicy) LendingPolicy {
	p.Models = normalizeList(p.Models)
	return p
}

func validateLending(p LendingPolicy) (LendingPolicy, error) {
	p = normalizeLending(p)
	l := p.Limits
	if l.RequestsPerMinute < 0 || l.ConcurrentStreams < 0 || l.MaxInputBytes < 0 || l.MaxOutputTokens < 0 || l.MediaJobs < 0 || l.BudgetUSDPerDay < 0 {
		return p, newError(http.StatusBadRequest, CodeInvalidRequest, "lending limits must not be negative")
	}
	return p, nil
}

func normalizeBorrowing(p BorrowingPolicy) BorrowingPolicy {
	p.Mode = strings.ToLower(strings.TrimSpace(p.Mode))
	if p.Mode != ModeShared {
		p.Mode = ModeFallback
	}
	return p
}

func normalizeShared(sel SharedSelection) SharedSelection {
	sel.Models = normalizeList(sel.Models)
	sel.Combos = normalizeList(sel.Combos)
	sel.APIKeys = normalizeList(sel.APIKeys)
	if len(sel.Prices) == 0 {
		sel.Prices = nil
		return sel
	}
	prices := make(map[string]config.PricingConfig, len(sel.Prices))
	for id, price := range sel.Prices {
		if id = strings.TrimSpace(id); id != "" {
			prices[id] = price
		}
	}
	sel.Prices = prices
	return sel
}

func validateShared(sel SharedSelection) (SharedSelection, error) {
	sel = normalizeShared(sel)
	for id, price := range sel.Prices {
		if price.InputPerMillion < 0 || price.OutputPerMillion < 0 || price.ReasoningPerMillion < 0 || price.Request < 0 {
			return sel, newError(http.StatusBadRequest, CodeInvalidRequest, "price for "+id+" must not be negative")
		}
	}
	return sel, nil
}

// normalizeCatalog sorts entries so the version hash does not depend on the
// order BuildCatalog happened to produce, and replaces nil lists with empty
// ones for JSON.
func normalizeCatalog(c Catalog) Catalog {
	c.Models = slices.Clone(c.Models)
	if c.Models == nil {
		c.Models = []CatalogModel{}
	}
	slices.SortStableFunc(c.Models, func(a, b CatalogModel) int { return strings.Compare(a.ID, b.ID) })
	c.Combos = slices.Clone(c.Combos)
	if c.Combos == nil {
		c.Combos = []CatalogCombo{}
	}
	slices.SortStableFunc(c.Combos, func(a, b CatalogCombo) int { return strings.Compare(a.ID, b.ID) })
	for i := range c.Combos {
		items := slices.Clone(c.Combos[i].Items)
		if items == nil {
			items = []CatalogComboItem{}
		}
		slices.SortStableFunc(items, func(a, b CatalogComboItem) int { return a.Position - b.Position })
		c.Combos[i].Items = items
	}
	c.APIKeys = slices.Clone(c.APIKeys)
	if c.APIKeys == nil {
		c.APIKeys = []CatalogAPIKey{}
	}
	slices.SortStableFunc(c.APIKeys, func(a, b CatalogAPIKey) int { return strings.Compare(a.ID, b.ID) })
	for i := range c.APIKeys {
		if c.APIKeys[i].Models == nil {
			c.APIKeys[i].Models = []string{}
		}
	}
	return c
}

func cloneOffers(offers []Offer) []Offer {
	if offers == nil {
		return []Offer{}
	}
	return slices.Clone(offers)
}

// contextMutex is a one-slot lock whose wait can be canceled.
type contextMutex chan struct{}

func (m contextMutex) Lock()   { m <- struct{}{} }
func (m contextMutex) Unlock() { <-m }
func (m contextMutex) LockContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case m <- struct{}{}:
		return nil
	}
}
