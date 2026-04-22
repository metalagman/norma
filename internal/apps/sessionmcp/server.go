package sessionmcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverName      = "norma-session-state"
	serverVersion   = "1.0.0"
	defaultToolName = "state"

	codeValidationError = "validation_error"
	codeBackendError    = "backend_error"
)

type serverConfig struct {
	toolPrefix string
}

// ServerOption customizes server construction.
type ServerOption func(*serverConfig)

// WithToolPrefix configures MCP tool names (for example: "state" -> "state.get").
func WithToolPrefix(prefix string) ServerOption {
	return func(cfg *serverConfig) {
		cfg.toolPrefix = strings.TrimSpace(prefix)
	}
}

func resolveServerConfig(opts ...ServerOption) (serverConfig, error) {
	cfg := serverConfig{toolPrefix: defaultToolName}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	cfg.toolPrefix = strings.TrimSpace(cfg.toolPrefix)
	if cfg.toolPrefix == "" {
		return serverConfig{}, fmt.Errorf("tool prefix is required")
	}
	return cfg, nil
}

func buildServerInstructions(toolPrefix string) string {
	return fmt.Sprintf(`Use this server to persist session state.

- %s.* reads and writes session or app key-value data.
- %s.ns_* scopes keys under an explicit namespace such as a session ID or agent name.
- %s.clear deletes all data owned by this server and is destructive.
- %s.get_json, set_json, and merge_json are for JSON values rather than raw strings.`, toolPrefix, toolPrefix, toolPrefix, toolPrefix)
}

// Run serves the session state MCP server over stdio.
func Run(ctx context.Context, store Store, opts ...ServerOption) error {
	server, err := NewServer(store, opts...)
	if err != nil {
		return err
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

// RunHTTP serves the session state MCP server over HTTP.
func RunHTTP(ctx context.Context, store Store, addr string, opts ...ServerOption) error {
	result, err := StartHTTPServer(ctx, store, addr, opts...)
	if err != nil {
		return err
	}
	<-ctx.Done()
	return result.Close()
}

// HTTPServerResult contains the address and cleanup function for an embedded HTTP server.
type HTTPServerResult struct {
	// Addr is the actual listen address (e.g., "127.0.0.1:54321").
	Addr string
	// Close shuts down the server.
	Close func() error
}

// StartHTTPServer starts an HTTP server on the given address and returns immediately.
// Use ":0" to let the OS assign a random port.
func StartHTTPServer(ctx context.Context, store Store, addr string, opts ...ServerOption) (*HTTPServerResult, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	if strings.TrimSpace(addr) == "" {
		return nil, fmt.Errorf("address is required")
	}

	cfg, err := resolveServerConfig(opts...)
	if err != nil {
		return nil, err
	}

	getServer := func(_ *http.Request) *mcp.Server {
		server, newErr := newServerWithConfig(store, cfg)
		if newErr != nil {
			return nil
		}
		return server
	}

	handler := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{})

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", addr, err)
	}

	actualAddr := listener.Addr().String()
	httpServer := &http.Server{Handler: handler}

	go func() {
		<-ctx.Done()
		_ = httpServer.Close()
	}()

	go func() {
		_ = httpServer.Serve(listener)
	}()

	return &HTTPServerResult{
		Addr: actualAddr,
		Close: func() error {
			return httpServer.Close()
		},
	}, nil
}

// NewServer builds the session state MCP server.
func NewServer(store Store, opts ...ServerOption) (*mcp.Server, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}

	cfg, err := resolveServerConfig(opts...)
	if err != nil {
		return nil, err
	}
	return newServerWithConfig(store, cfg)
}

func newServerWithConfig(store Store, cfg serverConfig) (*mcp.Server, error) {
	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    serverName,
			Version: serverVersion,
		},
		&mcp.ServerOptions{Instructions: buildServerInstructions(cfg.toolPrefix)},
	)

	RegisterTools(server, store, WithToolPrefix(cfg.toolPrefix))
	return server, nil
}

// RegisterTools adds session-state MCP tools to an existing server.
func RegisterTools(server *mcp.Server, store Store, opts ...ServerOption) {
	if server == nil || store == nil {
		return
	}
	cfg, err := resolveServerConfig(opts...)
	if err != nil {
		return
	}
	svc := &service{store: store, toolPrefix: cfg.toolPrefix}
	svc.registerTools(server)
}

type service struct {
	store      Store
	toolPrefix string
}

func (s *service) toolName(op string) string {
	return fmt.Sprintf("%s.%s", s.toolPrefix, op)
}

func (s *service) registerTools(server *mcp.Server) {
	// Basic key-value operations
	mcp.AddTool(server, &mcp.Tool{Name: s.toolName("get"), Description: "Read a raw string value from persistent state by exact key."}, s.getKey)
	mcp.AddTool(server, &mcp.Tool{Name: s.toolName("set"), Description: "Write a raw string value to persistent state under an exact key."}, s.setKey)
	mcp.AddTool(server, &mcp.Tool{Name: s.toolName("delete"), Description: "Delete one exact key from persistent state."}, s.deleteKey)
	mcp.AddTool(server, &mcp.Tool{Name: s.toolName("list"), Description: "List persistent-state keys, optionally restricted to a prefix."}, s.listKeys)
	mcp.AddTool(server, &mcp.Tool{Name: s.toolName("clear"), Description: fmt.Sprintf("Delete all keys stored by %s.* tools. This is destructive and affects every session using this state store.", s.toolPrefix)}, s.clearState)

	// JSON operations
	mcp.AddTool(server, &mcp.Tool{Name: s.toolName("get_json"), Description: "Read a key from persistent state and return its parsed JSON value."}, s.getJSON)
	mcp.AddTool(server, &mcp.Tool{Name: s.toolName("set_json"), Description: "Write a JSON value to persistent state under an exact key."}, s.setJSON)
	mcp.AddTool(server, &mcp.Tool{Name: s.toolName("merge_json"), Description: "Merge object fields into an existing JSON object stored at a key and return the merged object."}, s.mergeJSON)

	// Namespaced operations for agent/session isolation
	mcp.AddTool(server, &mcp.Tool{Name: s.toolName("ns_get"), Description: "Read a raw string value from a namespace-scoped key. Use namespaces such as session IDs or agent names to avoid collisions."}, s.nsGet)
	mcp.AddTool(server, &mcp.Tool{Name: s.toolName("ns_set"), Description: "Write a raw string value to a namespace-scoped key for session or agent isolation."}, s.nsSet)
	mcp.AddTool(server, &mcp.Tool{Name: s.toolName("ns_set_json"), Description: "Write a JSON value to a namespace-scoped key for session or agent isolation."}, s.nsSetJSON)
	mcp.AddTool(server, &mcp.Tool{Name: s.toolName("ns_list"), Description: "List keys stored inside one namespace without returning keys from other namespaces."}, s.nsList)
}

// nsKey builds a namespaced key for isolation.
func nsKey(namespace, key string) string {
	return fmt.Sprintf("ns:%s:%s", strings.TrimSpace(namespace), strings.TrimSpace(key))
}

// Basic key-value tools

func (s *service) getKey(ctx context.Context, _ *mcp.CallToolRequest, in getKeyInput) (*mcp.CallToolResult, getKeyOutput, error) {
	toolName := s.toolName("get")
	key := strings.TrimSpace(in.Key)
	if key == "" {
		result, out := validationFailure(toolName, "key is required")
		return result, getKeyOutput{ToolOutcome: out}, nil
	}

	value, ok, err := s.store.Get(ctx, key)
	if err != nil {
		result, out := backendFailure(toolName, err)
		return result, getKeyOutput{ToolOutcome: out}, nil
	}
	return nil, getKeyOutput{ToolOutcome: okOutcome(), Value: value, Found: ok}, nil
}

func (s *service) setKey(ctx context.Context, _ *mcp.CallToolRequest, in setKeyInput) (*mcp.CallToolResult, basicOutput, error) {
	toolName := s.toolName("set")
	key := strings.TrimSpace(in.Key)
	if key == "" {
		result, out := validationFailure(toolName, "key is required")
		return result, basicOutput{ToolOutcome: out}, nil
	}

	if err := s.store.Set(ctx, key, in.Value); err != nil {
		result, out := backendFailure(toolName, err)
		return result, basicOutput{ToolOutcome: out}, nil
	}
	return nil, basicOutput{ToolOutcome: okOutcome()}, nil
}

func (s *service) deleteKey(ctx context.Context, _ *mcp.CallToolRequest, in deleteKeyInput) (*mcp.CallToolResult, basicOutput, error) {
	toolName := s.toolName("delete")
	key := strings.TrimSpace(in.Key)
	if key == "" {
		result, out := validationFailure(toolName, "key is required")
		return result, basicOutput{ToolOutcome: out}, nil
	}

	if err := s.store.Delete(ctx, key); err != nil {
		result, out := backendFailure(toolName, err)
		return result, basicOutput{ToolOutcome: out}, nil
	}
	return nil, basicOutput{ToolOutcome: okOutcome()}, nil
}

func (s *service) listKeys(ctx context.Context, _ *mcp.CallToolRequest, in listKeysInput) (*mcp.CallToolResult, listKeysOutput, error) {
	toolName := s.toolName("list")
	prefix := strings.TrimSpace(in.Prefix)

	keys, err := s.store.List(ctx, prefix)
	if err != nil {
		result, out := backendFailure(toolName, err)
		return result, listKeysOutput{ToolOutcome: out}, nil
	}
	return nil, listKeysOutput{ToolOutcome: okOutcome(), Keys: keys}, nil
}

func (s *service) clearState(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, basicOutput, error) {
	toolName := s.toolName("clear")
	if err := s.store.Clear(ctx); err != nil {
		result, out := backendFailure(toolName, err)
		return result, basicOutput{ToolOutcome: out}, nil
	}
	return nil, basicOutput{ToolOutcome: okOutcome()}, nil
}

// JSON tools

func (s *service) getJSON(ctx context.Context, _ *mcp.CallToolRequest, in getJSONInput) (*mcp.CallToolResult, getJSONOutput, error) {
	toolName := s.toolName("get_json")
	key := strings.TrimSpace(in.Key)
	if key == "" {
		result, out := validationFailure(toolName, "key is required")
		return result, getJSONOutput{ToolOutcome: out}, nil
	}

	value, ok, err := s.store.GetJSON(ctx, key)
	if err != nil {
		result, out := backendFailure(toolName, err)
		return result, getJSONOutput{ToolOutcome: out}, nil
	}
	return nil, getJSONOutput{ToolOutcome: okOutcome(), Value: value, Found: ok}, nil
}

func (s *service) setJSON(ctx context.Context, _ *mcp.CallToolRequest, in setJSONInput) (*mcp.CallToolResult, basicOutput, error) {
	toolName := s.toolName("set_json")
	key := strings.TrimSpace(in.Key)
	if key == "" {
		result, out := validationFailure(toolName, "key is required")
		return result, basicOutput{ToolOutcome: out}, nil
	}

	if err := s.store.SetJSON(ctx, key, in.Value); err != nil {
		result, out := backendFailure(toolName, err)
		return result, basicOutput{ToolOutcome: out}, nil
	}
	return nil, basicOutput{ToolOutcome: okOutcome()}, nil
}

func (s *service) mergeJSON(ctx context.Context, _ *mcp.CallToolRequest, in mergeJSONInput) (*mcp.CallToolResult, mergeJSONOutput, error) {
	toolName := s.toolName("merge_json")
	key := strings.TrimSpace(in.Key)
	if key == "" {
		result, out := validationFailure(toolName, "key is required")
		return result, mergeJSONOutput{ToolOutcome: out}, nil
	}
	if len(in.Value) == 0 {
		result, out := validationFailure(toolName, "value must have at least one field")
		return result, mergeJSONOutput{ToolOutcome: out}, nil
	}

	merged, err := s.store.MergeJSON(ctx, key, in.Value)
	if err != nil {
		result, out := backendFailure(toolName, err)
		return result, mergeJSONOutput{ToolOutcome: out}, nil
	}
	return nil, mergeJSONOutput{ToolOutcome: okOutcome(), Merged: merged}, nil
}

// Namespaced tools

func (s *service) nsGet(ctx context.Context, _ *mcp.CallToolRequest, in keyspaceInput) (*mcp.CallToolResult, getKeyOutput, error) {
	toolName := s.toolName("ns_get")
	namespace := strings.TrimSpace(in.Namespace)
	if namespace == "" {
		result, out := validationFailure(toolName, "namespace is required")
		return result, getKeyOutput{ToolOutcome: out}, nil
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		result, out := validationFailure(toolName, "key is required")
		return result, getKeyOutput{ToolOutcome: out}, nil
	}

	value, ok, err := s.store.Get(ctx, nsKey(namespace, key))
	if err != nil {
		result, out := backendFailure(toolName, err)
		return result, getKeyOutput{ToolOutcome: out}, nil
	}
	return nil, getKeyOutput{ToolOutcome: okOutcome(), Value: value, Found: ok}, nil
}

func (s *service) nsSet(ctx context.Context, _ *mcp.CallToolRequest, in keyspaceValueInput) (*mcp.CallToolResult, basicOutput, error) {
	toolName := s.toolName("ns_set")
	namespace := strings.TrimSpace(in.Namespace)
	if namespace == "" {
		result, out := validationFailure(toolName, "namespace is required")
		return result, basicOutput{ToolOutcome: out}, nil
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		result, out := validationFailure(toolName, "key is required")
		return result, basicOutput{ToolOutcome: out}, nil
	}

	if err := s.store.Set(ctx, nsKey(namespace, key), in.Value); err != nil {
		result, out := backendFailure(toolName, err)
		return result, basicOutput{ToolOutcome: out}, nil
	}
	return nil, basicOutput{ToolOutcome: okOutcome()}, nil
}

func (s *service) nsSetJSON(ctx context.Context, _ *mcp.CallToolRequest, in keyspaceJSONInput) (*mcp.CallToolResult, basicOutput, error) {
	toolName := s.toolName("ns_set_json")
	namespace := strings.TrimSpace(in.Namespace)
	if namespace == "" {
		result, out := validationFailure(toolName, "namespace is required")
		return result, basicOutput{ToolOutcome: out}, nil
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		result, out := validationFailure(toolName, "key is required")
		return result, basicOutput{ToolOutcome: out}, nil
	}

	if err := s.store.SetJSON(ctx, nsKey(namespace, key), in.Value); err != nil {
		result, out := backendFailure(toolName, err)
		return result, basicOutput{ToolOutcome: out}, nil
	}
	return nil, basicOutput{ToolOutcome: okOutcome()}, nil
}

func (s *service) nsList(ctx context.Context, _ *mcp.CallToolRequest, in namespaceOnlyInput) (*mcp.CallToolResult, listKeysOutput, error) {
	toolName := s.toolName("ns_list")
	namespace := strings.TrimSpace(in.Namespace)
	if namespace == "" {
		result, out := validationFailure(toolName, "namespace is required")
		return result, listKeysOutput{ToolOutcome: out}, nil
	}

	prefix := nsKey(namespace, "")
	keys, err := s.store.List(ctx, prefix)
	if err != nil {
		result, out := backendFailure(toolName, err)
		return result, listKeysOutput{ToolOutcome: out}, nil
	}

	// Strip prefix from returned keys
	stripped := make([]string, 0, len(keys))
	for _, k := range keys {
		if after, ok := strings.CutPrefix(k, prefix); ok {
			stripped = append(stripped, after)
		}
	}
	return nil, listKeysOutput{ToolOutcome: okOutcome(), Keys: stripped}, nil
}

// Helpers

func okOutcome() ToolOutcome {
	return ToolOutcome{OK: true}
}

func validationFailure(operation string, message string) (*mcp.CallToolResult, ToolOutcome) {
	return failure(operation, codeValidationError, message)
}

func backendFailure(operation string, err error) (*mcp.CallToolResult, ToolOutcome) {
	return failure(operation, codeBackendError, err.Error())
}

func failure(operation string, code string, message string) (*mcp.CallToolResult, ToolOutcome) {
	return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: message}},
		}, ToolOutcome{
			OK: false,
			Error: &ToolError{
				Operation: operation,
				Code:      code,
				Message:   message,
			},
		}
}
