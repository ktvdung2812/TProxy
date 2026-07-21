package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func runConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	remoteURL := fs.String("url", "http://127.0.0.1:28120", "remote tproxy base URL")
	localAddr := fs.String("listen", "127.0.0.1:28122", "local listen address")
	apiKey := fs.String("key", "", "client API key forwarded to remote tproxy")
	if err := fs.Parse(args); err != nil {
		return err
	}
	target, err := url.Parse(strings.TrimRight(*remoteURL, "/"))
	if err != nil {
		return fmt.Errorf("invalid remote url: %w", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		originalDirector(r)
		r.Host = target.Host
		if strings.TrimSpace(*apiKey) != "" {
			r.Header.Set("Authorization", "Bearer "+strings.TrimSpace(*apiKey))
		}
		r.Header.Set("X-TProxy-Remote-CLI", "connect")
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "remote tproxy unreachable: "+err.Error(), http.StatusBadGateway)
	}
	server := &http.Server{
		Addr:              *localAddr,
		Handler:           proxy,
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", *localAddr)
	if err != nil {
		return err
	}
	defer listener.Close()
	log.Printf("tproxy connect listening on http://%s -> %s", *localAddr, target)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err = <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func isConnectCommand(args []string) bool {
	return len(args) > 0 && args[0] == "connect"
}

// discardWriter prevents flag package from printing usage to stdout during connect parsing.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func initConnectFlags(args []string) {
	if !isConnectCommand(args) {
		return
	}
	flag.CommandLine.SetOutput(io.Discard)
}

func maybeRunConnect(args []string) (bool, error) {
	if !isConnectCommand(args) {
		return false, nil
	}
	if err := runConnect(args[1:]); err != nil {
		return true, err
	}
	return true, nil
}

func connectFromEnv() string {
	return strings.TrimSpace(os.Getenv("TPROXY_REMOTE_URL"))
}
