package configmcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/normahq/norma/internal/config"
	"github.com/spf13/viper"
)

const (
	serverName     = "norma-config"
	serverVersion  = "1.0.0"
	defaultAddress = "127.0.0.1:9091"
	toolNamespace  = "relay.config"
)

type ToolError struct {
	Operation string `json:"operation"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

type ToolOutcome struct {
	OK    bool       `json:"ok"`
	Error *ToolError `json:"error,omitempty"`
}

func okOutcome() ToolOutcome {
	return ToolOutcome{OK: true}
}

func validationFailure(operation string, message string) (*mcp.CallToolResult, ToolOutcome) {
	return failure(operation, "validation_error", message)
}

func backendFailure(operation string, err error) (*mcp.CallToolResult, ToolOutcome) {
	return failure(operation, "backend_error", err.Error())
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

type ConfigService struct {
	configPath string
	viper      *viper.Viper
}

func NewConfigService(configPath string) (*ConfigService, error) {
	if configPath == "" {
		return nil, fmt.Errorf("config path is required")
	}

	v := viper.New()
	v.SetConfigFile(configPath)

	if err := v.ReadInConfig(); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	return &ConfigService{
		configPath: configPath,
		viper:      v,
	}, nil
}

func Run(ctx context.Context, svc *ConfigService) error {
	server, err := NewServer(svc)
	if err != nil {
		return err
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

func RunHTTP(ctx context.Context, svc *ConfigService, addr string) error {
	result, err := StartHTTPServer(ctx, svc, addr)
	if err != nil {
		return err
	}
	<-ctx.Done()
	return result.Close()
}

type HTTPServerResult struct {
	Addr  string
	Close func() error
}

func StartHTTPServer(ctx context.Context, svc *ConfigService, addr string) (*HTTPServerResult, error) {
	if svc == nil {
		return nil, fmt.Errorf("service is required")
	}
	address := strings.TrimSpace(addr)
	if address == "" {
		address = defaultAddress
	}

	getServer := func(_ *http.Request) *mcp.Server {
		server, err := NewServer(svc)
		if err != nil {
			return nil
		}
		return server
	}

	handler := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{})

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", address, err)
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

func NewServer(svc *ConfigService) (*mcp.Server, error) {
	if svc == nil {
		return nil, fmt.Errorf("service is required")
	}

	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    serverName,
			Version: serverVersion,
		},
		nil,
	)

	RegisterTools(server, svc)
	return server, nil
}

// RegisterTools adds config MCP tools to an existing server.
func RegisterTools(server *mcp.Server, svc *ConfigService) {
	if server == nil || svc == nil {
		return
	}
	svc.registerTools(server)
}

func toolName(name string) string {
	return toolNamespace + "." + name
}

func (s *ConfigService) registerTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolName("get"),
		Description: "Get a configuration value by key",
	}, s.getConfig)
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolName("set"),
		Description: "Set a configuration value by key",
	}, s.setConfig)
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolName("list"),
		Description: "List all configuration keys",
	}, s.listConfig)
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolName("delete"),
		Description: "Delete a configuration key",
	}, s.deleteConfig)
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolName("save"),
		Description: "Save configuration changes to file",
	}, s.saveConfig)
}

type getConfigInput struct {
	Key string `json:"key" jsonschema:"configuration key (supports dot notation for nested keys)"`
}

func (s *ConfigService) getConfig(ctx context.Context, _ *mcp.CallToolRequest, in getConfigInput) (*mcp.CallToolResult, ToolOutcome, error) {
	key := strings.TrimSpace(in.Key)
	if key == "" {
		result, out := validationFailure(toolName("get"), "key is required")
		return result, out, nil
	}

	value := s.viper.Get(key)
	if value == nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "Key not found"}},
		}, okOutcome(), nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%v", value)}},
	}, okOutcome(), nil
}

type setConfigInput struct {
	Key   string      `json:"key" jsonschema:"configuration key (supports dot notation for nested keys)"`
	Value interface{} `json:"value" jsonschema:"value to set"`
}

func (s *ConfigService) setConfig(ctx context.Context, _ *mcp.CallToolRequest, in setConfigInput) (*mcp.CallToolResult, ToolOutcome, error) {
	key := strings.TrimSpace(in.Key)
	if key == "" {
		result, out := validationFailure(toolName("set"), "key is required")
		return result, out, nil
	}

	s.viper.Set(key, in.Value)

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Set %s = %v", key, in.Value)}},
	}, okOutcome(), nil
}

type deleteConfigInput struct {
	Key string `json:"key" jsonschema:"configuration key to delete"`
}

func (s *ConfigService) deleteConfig(ctx context.Context, _ *mcp.CallToolRequest, in deleteConfigInput) (*mcp.CallToolResult, ToolOutcome, error) {
	key := strings.TrimSpace(in.Key)
	if key == "" {
		result, out := validationFailure(toolName("delete"), "key is required")
		return result, out, nil
	}

	s.viper.Set(key, nil)

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Deleted %s", key)}},
	}, okOutcome(), nil
}

func (s *ConfigService) listConfig(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ToolOutcome, error) {
	keys := s.viper.AllKeys()

	if len(keys) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "No configuration keys"}},
		}, okOutcome(), nil
	}

	var sb strings.Builder
	sb.WriteString("Configuration keys:\n")
	for _, k := range keys {
		fmt.Fprintf(&sb, "- %s\n", k)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: sb.String()}},
	}, okOutcome(), nil
}

type saveConfigInput struct {
	Path string `json:"path" jsonschema:"optional path to save config (defaults to original path)"`
}

func (s *ConfigService) saveConfig(ctx context.Context, _ *mcp.CallToolRequest, in saveConfigInput) (*mcp.CallToolResult, ToolOutcome, error) {
	path := strings.TrimSpace(in.Path)
	if path == "" {
		path = s.configPath
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		result, out := backendFailure(toolName("save"), err)
		return result, out, nil
	}

	ext := filepath.Ext(path)
	if ext == "" {
		path += ".yaml"
	}

	if err := s.viper.WriteConfigAs(path); err != nil {
		result, out := backendFailure(toolName("save"), err)
		return result, out, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Saved configuration to %s", path)}},
	}, okOutcome(), nil
}

func LoadConfig(workingDir string) (config.Config, error) {
	configPath := filepath.Join(workingDir, ".norma", config.CoreConfigFileName)
	v := viper.New()
	v.SetConfigFile(configPath)

	if err := v.ReadInConfig(); err != nil {
		if os.IsNotExist(err) {
			return config.Config{}, nil
		}
		return config.Config{}, fmt.Errorf("reading config: %w", err)
	}

	var cfg config.Config
	if err := v.Unmarshal(&cfg); err != nil {
		return config.Config{}, fmt.Errorf("unmarshalling config: %w", err)
	}

	return cfg, nil
}
