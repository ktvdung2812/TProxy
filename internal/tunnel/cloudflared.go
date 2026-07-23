package tunnel

import (
	"archive/tar"
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

type DownloadStatus struct {
	Downloading bool `json:"downloading"`
	Progress    int  `json:"progress"`
}

type Cloudflared struct {
	layout DataLayout

	mu              sync.Mutex
	process         *exec.Cmd
	intentionalKill bool
	onUnexpectedExit func()
	download        DownloadStatus
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

func (c *Cloudflared) Kill(localPort int) {
	c.mu.Lock()
	c.intentionalKill = true
	cmd := c.process
	c.process = nil
	c.onUnexpectedExit = nil
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

	cmd := exec.CommandContext(ctx, binary,
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

	var resolvedURL string
	var resolveOnce sync.Once
	handleChunk := func(chunk string) {
		appendLog(chunk)
		url := parseURL(chunk)
		if url == "" {
			return
		}
		resolveOnce.Do(func() {
			resolvedURL = url
			done <- result{url: url}
		})
		if url != resolvedURL && onURLUpdate != nil {
			resolvedURL = url
			onURLUpdate(url)
		}
	}

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				handleChunk(string(buf[:n]))
			}
			if err != nil {
				break
			}
		}
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				handleChunk(string(buf[:n]))
			}
			if err != nil {
				break
			}
		}
	}()

	go func() {
		err := cmd.Wait()
		_ = os.RemoveAll(configDir)
		_ = os.Remove(c.layout.CloudflaredPID)
		c.mu.Lock()
		intentional := c.intentionalKill
		handler := c.onUnexpectedExit
		c.process = nil
		c.mu.Unlock()
		if resolvedURL == "" {
			tailMu.Lock()
			tail := strings.TrimSpace(logTail.String())
			tailMu.Unlock()
			if tail == "" {
				tail = "(empty)"
			}
			done <- result{err: fmt.Errorf("cloudflared exited before URL ready: %v; log: %s", err, tail)}
			return
		}
		if !intentional && handler != nil {
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
