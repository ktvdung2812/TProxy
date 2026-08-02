package tunnel

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const minBinarySize = 1024 * 1024

var quickTunnelURLPattern = regexp.MustCompile(`https://([a-z0-9-]+)\.trycloudflare\.com`)
var cloudflaredConnectionIndexPattern = regexp.MustCompile(`\bconnIndex=([0-9]+)\b`)

type DownloadStatus struct {
	Downloading bool `json:"downloading"`
	Progress    int  `json:"progress"`
}

type Cloudflared struct {
	layout DataLayout

	mu               sync.Mutex
	process          *exec.Cmd
	intentionalKill  bool
	onUnexpectedExit func()
	download         DownloadStatus
	connections      map[string]struct{}
	connectionSerial int
}

type quickTunnelReadiness struct {
	mu         sync.Mutex
	url        string
	registered bool
}

func (r *quickTunnelReadiness) setURL(url string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if url == "" || url == r.url {
		return false
	}
	r.url = url
	return true
}

func (r *quickTunnelReadiness) markRegistered() {
	r.mu.Lock()
	r.registered = true
	r.mu.Unlock()
}

func (r *quickTunnelReadiness) snapshot() (url string, registered bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.url, r.registered
}

func NewCloudflared(layout DataLayout) *Cloudflared {
	return &Cloudflared{layout: layout}
}

func (c *Cloudflared) DownloadStatus() DownloadStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.download
}

func (c *Cloudflared) SetUnexpectedExitHandler(handler func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onUnexpectedExit = handler
}

func cloudflaredDownloadURL() (string, bool, error) {
	type mapping struct {
		archive bool
		name    string
	}
	platform := runtime.GOOS
	arch := runtime.GOARCH
	table := map[string]map[string]mapping{
		"darwin": {
			"amd64": {archive: true, name: "cloudflared-darwin-amd64.tgz"},
			"arm64": {archive: true, name: "cloudflared-darwin-arm64.tgz"},
		},
		"linux": {
			"amd64": {archive: false, name: "cloudflared-linux-amd64"},
			"arm64": {archive: false, name: "cloudflared-linux-arm64"},
		},
	}
	entry, ok := table[platform][arch]
	if !ok {
		return "", false, fmt.Errorf("unsupported platform %s/%s", platform, arch)
	}
	return "https://github.com/cloudflare/cloudflared/releases/latest/download/" + entry.name, entry.archive, nil
}

func isValidBinary(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.Size() < minBinarySize {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	buf := make([]byte, 4)
	if _, err := file.Read(buf); err != nil {
		return false
	}
	switch runtime.GOOS {
	case "windows":
		return buf[0] == 'M' && buf[1] == 'Z'
	case "darwin":
		return bytes.Equal(buf, []byte{0xcf, 0xfa, 0xed, 0xfe}) || bytes.Equal(buf, []byte{0xce, 0xfa, 0xed, 0xfe})
	default:
		return buf[0] == 0x7f && buf[1] == 'E' && buf[2] == 'L' && buf[3] == 'F'
	}
}

func (c *Cloudflared) Ensure(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.download.Downloading {
		c.mu.Unlock()
		time.Sleep(200 * time.Millisecond)
		return c.Ensure(ctx)
	}
	c.mu.Unlock()

	if err := os.MkdirAll(c.layout.BinDir, 0o755); err != nil {
		return "", err
	}
	if isValidBinary(c.layout.Cloudflared) {
		_ = os.Chmod(c.layout.Cloudflared, 0o755)
		return c.layout.Cloudflared, nil
	}
	_ = os.Remove(c.layout.Cloudflared)

	url, isArchive, err := cloudflaredDownloadURL()
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	c.download = DownloadStatus{Downloading: true, Progress: 0}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.download = DownloadStatus{}
		c.mu.Unlock()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download cloudflared: HTTP %d", resp.StatusCode)
	}

	tmpPath := c.layout.Cloudflared + ".tmp"
	file, err := os.Create(tmpPath)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	if isArchive {
		if err := extractCloudflaredFromTgz(tmpPath, c.layout.Cloudflared); err != nil {
			_ = os.Remove(tmpPath)
			return "", err
		}
		_ = os.Remove(tmpPath)
	} else if err := os.Rename(tmpPath, c.layout.Cloudflared); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Chmod(c.layout.Cloudflared, 0o755); err != nil {
		return "", err
	}
	if !isValidBinary(c.layout.Cloudflared) {
		_ = os.Remove(c.layout.Cloudflared)
		return "", fmt.Errorf("downloaded cloudflared binary is invalid")
	}
	return c.layout.Cloudflared, nil
}

func extractCloudflaredFromTgz(archivePath, dest string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg || header.Name != "cloudflared" {
			continue
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	}
	return fmt.Errorf("cloudflared binary not found in archive")
}

func (c *Cloudflared) IsRunning() bool {
	pid, err := loadPID(c.layout.CloudflaredPID)
	if err != nil || pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// IsConnected reports whether the cloudflared process that this service
// started currently has at least one registered Cloudflare edge connection.
// A live process alone is not sufficient: Cloudflare returns error 1033 when
// the connector has lost every edge connection but has not exited yet.
func (c *Cloudflared) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.process != nil && len(c.connections) > 0
}

func (c *Cloudflared) resetConnectionsLocked() {
	c.connections = make(map[string]struct{})
	c.connectionSerial = 0
}

// observeTunnelConnectionLog keeps a small connection-state model from the
// structured cloudflared log lines. The connIndex is stable for each HA
// connection, so multiple registrations for the same index do not inflate the
// count. If an older cloudflared build omits connIndex, we still keep a best
// effort count and restart conservatively when it disconnects.
func (c *Cloudflared) observeTunnelConnectionLog(line string) {
	message := strings.ToLower(line)
	registered := strings.Contains(message, "registered tunnel connection")
	unregistered := strings.Contains(message, "unregistered tunnel connection")
	if !registered && !unregistered {
		return
	}

	connectionID := ""
	if match := cloudflaredConnectionIndexPattern.FindStringSubmatch(line); len(match) == 2 {
		connectionID = match[1]
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connections == nil {
		c.resetConnectionsLocked()
	}
	if unregistered {
		if connectionID != "" {
			delete(c.connections, connectionID)
			return
		}
		for id := range c.connections {
			delete(c.connections, id)
			break
		}
		return
	}
	if connectionID == "" {
		c.connectionSerial++
		connectionID = fmt.Sprintf("unknown-%d", c.connectionSerial)
	}
	c.connections[connectionID] = struct{}{}
}

func (c *Cloudflared) Kill(localPort int) {
	c.mu.Lock()
	c.intentionalKill = true
	cmd := c.process
	c.process = nil
	c.resetConnectionsLocked()
	c.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if pid, err := loadPID(c.layout.CloudflaredPID); err == nil && pid > 0 {
		if process, err := os.FindProcess(pid); err == nil && process != nil {
			_ = process.Kill()
		}
	}
	_ = os.Remove(c.layout.CloudflaredPID)
	killCloudflaredByPort(localPort)
}

// takeUnexpectedExitHandler detaches the process that exited and returns the
// restart callback only when that exact process was still current and was not
// deliberately stopped. A replacement process must never trigger a restart
// because its predecessor happened to exit afterward.
func (c *Cloudflared) takeUnexpectedExitHandler(cmd *exec.Cmd) func() {
	handler, _ := c.finishProcess(cmd)
	return handler
}

func (c *Cloudflared) finishProcess(cmd *exec.Cmd) (handler func(), wasCurrent bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.process != cmd {
		return nil, false
	}
	c.process = nil
	c.resetConnectionsLocked()
	if c.intentionalKill {
		return nil, true
	}
	return c.onUnexpectedExit, true
}

func killCloudflaredByPort(port int) {
	if port <= 0 {
		return
	}
	if runtime.GOOS == "windows" {
		return
	}
	pattern := fmt.Sprintf("cloudflared.*:%d([^0-9]|$)", port)
	_ = exec.Command("pkill", "-f", pattern).Run()
}

func (c *Cloudflared) SpawnQuickTunnel(ctx context.Context, localPort int, onURLUpdate func(string)) (string, error) {
	binary, err := c.Ensure(ctx)
	if err != nil {
		return "", err
	}
	configDir, err := os.MkdirTemp("", "tproxy-cloudflared-")
	if err != nil {
		return "", err
	}
	configPath := filepath.Join(configDir, "config.yml")
	if err := os.WriteFile(configPath, []byte("# quick-tunnel config placeholder\n"), 0o644); err != nil {
		_ = os.RemoveAll(configDir)
		return "", err
	}

	// Do not bind cloudflared to the admin request context. The API request
	// naturally ends once the URL is returned, whereas the connector must keep
	// running until it is explicitly disabled or exits unexpectedly.
	cmd := exec.Command(binary,
		"tunnel", "--url", fmt.Sprintf("http://127.0.0.1:%d", localPort),
		"--config", configPath,
		"--no-autoupdate",
		"--retries", "99",
	)
	cmd.Env = append(os.Environ(), "TUNNEL_TRANSPORT_PROTOCOL="+QuickTunnelProtocol())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = os.RemoveAll(configDir)
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = os.RemoveAll(configDir)
		return "", err
	}
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(configDir)
		return "", err
	}
	if cmd.Process != nil {
		_ = savePID(c.layout.CloudflaredPID, cmd.Process.Pid)
	}

	c.mu.Lock()
	c.process = cmd
	c.intentionalKill = false
	c.resetConnectionsLocked()
	c.mu.Unlock()

	type result struct {
		url string
		err error
	}
	done := make(chan result, 1)
	logTail := &strings.Builder{}
	var tailMu sync.Mutex

	appendLog := func(chunk string) {
		tailMu.Lock()
		defer tailMu.Unlock()
		logTail.WriteString(chunk)
		if logTail.Len() > 4000 {
			trimmed := logTail.String()
			logTail.Reset()
			logTail.WriteString(trimmed[len(trimmed)-4000:])
		}
	}

	parseURL := func(message string) string {
		matches := quickTunnelURLPattern.FindAllStringSubmatch(message, -1)
		for i := len(matches) - 1; i >= 0; i-- {
			if len(matches[i]) > 1 && matches[i][1] != "api" {
				return matches[i][0]
			}
		}
		return ""
	}

	readiness := &quickTunnelReadiness{}
	var readyOnce sync.Once
	markReady := func() {
		url, registered := readiness.snapshot()
		if url != "" && registered {
			readyOnce.Do(func() {
				done <- result{url: url}
			})
		}
	}
	handleLine := func(line string) {
		appendLog(line + "\n")
		c.observeTunnelConnectionLog(line)
		if strings.Contains(strings.ToLower(line), "registered tunnel connection") &&
			!strings.Contains(strings.ToLower(line), "unregistered tunnel connection") {
			readiness.markRegistered()
		}
		url := parseURL(line)
		if readiness.setURL(url) && onURLUpdate != nil {
			onURLUpdate(url)
		}
		markReady()
	}

	var logReaders sync.WaitGroup
	readLogs := func(reader io.Reader) {
		defer logReaders.Done()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 256*1024)
		for scanner.Scan() {
			handleLine(scanner.Text())
		}
	}
	logReaders.Add(2)
	go readLogs(stdout)
	go readLogs(stderr)

	go func() {
		err := cmd.Wait()
		logReaders.Wait()
		_ = os.RemoveAll(configDir)
		handler, wasCurrent := c.finishProcess(cmd)
		if wasCurrent {
			_ = os.Remove(c.layout.CloudflaredPID)
		}
		resolvedURL, registered := readiness.snapshot()
		if resolvedURL == "" || !registered {
			tailMu.Lock()
			tail := strings.TrimSpace(logTail.String())
			tailMu.Unlock()
			if tail == "" {
				tail = "(empty)"
			}
			done <- result{err: fmt.Errorf("cloudflared exited before tunnel connection was ready: %v; log: %s", err, tail)}
			return
		}
		if handler != nil {
			handler()
		}
	}()

	select {
	case <-ctx.Done():
		c.Kill(localPort)
		return "", ctx.Err()
	case res := <-done:
		if res.err != nil {
			c.Kill(localPort)
			return "", res.err
		}
		log.Printf("[tunnel] cloudflared URL: %s", res.url)
		return res.url, nil
	case <-time.After(90 * time.Second):
		c.Kill(localPort)
		tailMu.Lock()
		tail := strings.TrimSpace(logTail.String())
		tailMu.Unlock()
		return "", fmt.Errorf("quick tunnel timed out; last log: %s", tail)
	}
}

func savePID(path string, pid int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o644)
}

func loadPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}
