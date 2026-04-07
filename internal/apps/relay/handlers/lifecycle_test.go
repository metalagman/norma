package handlers

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/normahq/norma/internal/apps/relay/session"
	"github.com/normahq/norma/internal/apps/sessionmcp"
	"github.com/normahq/norma/pkg/runtime/mcpregistry"
	"github.com/rs/zerolog"
)

func TestIsBundled(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{id: "norma.config", want: true},
		{id: "norma.state", want: true},
		{id: "norma.relay", want: true},
		{id: "norma.workspace", want: true},
		{id: "norma.tasks", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			if got := isBundled(tc.id); got != tc.want {
				t.Fatalf("isBundled(%q) = %t, want %t", tc.id, got, tc.want)
			}
		})
	}
}

func TestSelectConfigPath_RelayUsesDedicatedConfigPath(t *testing.T) {
	workDir := t.TempDir()
	got := selectConfigPath(workDir, "relay")
	want := filepath.Join(workDir, ".config", "relay", "config.yaml")
	if got != want {
		t.Fatalf("selectConfigPath() = %q, want %q", got, want)
	}
}

func TestSelectConfigPath_NonRelayFallsBackToCoreConfig(t *testing.T) {
	workDir := t.TempDir()
	normaDir := filepath.Join(workDir, ".norma")
	if err := os.MkdirAll(normaDir, 0o755); err != nil {
		t.Fatalf("mkdir .norma: %v", err)
	}
	if err := os.WriteFile(filepath.Join(normaDir, "config.yaml"), []byte("a: b\n"), 0o600); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}

	got := selectConfigPath(workDir, "cli")
	want := filepath.Join(normaDir, "config.yaml")
	if got != want {
		t.Fatalf("selectConfigPath() = %q, want %q", got, want)
	}
}

func TestBundledRegistryURL(t *testing.T) {
	addr := "127.0.0.1:9010"
	if got := bundledRegistryURL(addr, "norma.relay"); got != "http://127.0.0.1:9010/mcp" {
		t.Fatalf("bundledRegistryURL(relay) = %q, want http://127.0.0.1:9010/mcp", got)
	}
	if got := bundledRegistryURL(addr, "norma.state"); got != "http://127.0.0.1:9010/mcp/norma.state" {
		t.Fatalf("bundledRegistryURL(state) = %q, want http://127.0.0.1:9010/mcp/norma.state", got)
	}
}

func TestStartBundledMCPHTTPServer_MountsRoutesAndAlias(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mkHandler := func(text string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, text)
		})
	}

	res, err := startBundledMCPHTTPServer(ctx, "127.0.0.1:0", map[string]http.Handler{
		"norma.config": mkHandler("config"),
		"norma.relay":  mkHandler("relay"),
	})
	if err != nil {
		t.Fatalf("startBundledMCPHTTPServer() error = %v", err)
	}
	t.Cleanup(func() {
		_ = res.Close()
	})

	assertBody := func(path, want string) {
		t.Helper()
		resp, err := http.Get("http://" + res.Addr + path)
		if err != nil {
			t.Fatalf("GET %s error = %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body for %s: %v", path, err)
		}
		if got := string(body); got != want {
			t.Fatalf("GET %s body = %q, want %q", path, got, want)
		}
	}

	assertBody("/mcp/norma.config", "config")
	assertBody("/mcp/norma.relay", "relay")
	assertBody("/mcp", "relay")
}

func TestEnsureBundledServers_RegistersSharedListenerURLs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workDir := t.TempDir()
	manager := &InternalMCPManager{
		workspaceEnabled: true,
		logger:           zerolog.Nop(),
		registry:         mcpregistry.New(nil),
		workingDir:       workDir,
		sessionManager:   &session.Manager{},
		stateStore:       sessionmcp.NewMemoryStore(),
	}

	if err := manager.ensureBundledServers(ctx, ""); err != nil {
		t.Fatalf("ensureBundledServers() error = %v", err)
	}
	t.Cleanup(func() {
		for _, cleanup := range manager.cleanups {
			_ = cleanup()
		}
	})

	wantPaths := map[string]string{
		"norma.config":    "/mcp/norma.config",
		"norma.state":     "/mcp/norma.state",
		"norma.relay":     "/mcp",
		"norma.workspace": "/mcp/norma.workspace",
	}

	var sharedHost string
	for id, wantPath := range wantPaths {
		cfg, ok := manager.registry.Get(id)
		if !ok {
			t.Fatalf("registry missing %s", id)
		}
		u, err := url.Parse(cfg.URL)
		if err != nil {
			t.Fatalf("parse URL for %s: %v", id, err)
		}
		if u.Scheme != "http" {
			t.Fatalf("%s scheme = %q, want http", id, u.Scheme)
		}
		if u.Path != wantPath {
			t.Fatalf("%s path = %q, want %q", id, u.Path, wantPath)
		}
		if sharedHost == "" {
			sharedHost = u.Host
		} else if u.Host != sharedHost {
			t.Fatalf("%s host = %q, want shared host %q", id, u.Host, sharedHost)
		}
	}

	if strings.TrimSpace(sharedHost) == "" {
		t.Fatal("shared host is empty")
	}
}
