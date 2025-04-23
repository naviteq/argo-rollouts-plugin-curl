package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"net/rpc"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
)

// ---- Handshake config ----
var handshake = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "ARGO_ROLLOUTS_PLUGIN",
	MagicCookieValue: "step",
}

// ---- Input / Output Structs ----
type PluginInput struct {
	Config map[string]string `json:"config"`
}

type PluginOutput struct {
	Message string `json:"message"`
	Success bool   `json:"success"`
}

// ---- StepPlugin Interface ----
type StepPlugin interface {
	Run(ctx context.Context, rawInput json.RawMessage) (json.RawMessage, error)
}

// ---- Plugin Implementation ----
type HTTPPlugin struct{}

func (p *HTTPPlugin) Run(ctx context.Context, rawInput json.RawMessage) (json.RawMessage, error) {
	var input PluginInput
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return nil, fmt.Errorf("failed to parse input: %w", err)
	}

	uri, ok1 := input.Config["uri"]
	method, ok2 := input.Config["method"]
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("missing 'uri' or 'method' in config")
	}

	req, err := http.NewRequestWithContext(ctx, method, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return json.Marshal(PluginOutput{
			Message: fmt.Sprintf("Request error: %v", err),
			Success: false,
		})
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	result := PluginOutput{
		Message: fmt.Sprintf("Status: %s\nBody: %s", resp.Status, string(body)),
		Success: resp.StatusCode >= 200 && resp.StatusCode < 300,
	}

	return json.Marshal(result)
}

// ---- Plugin Wrapping ----
type HTTPStepPlugin struct {
	plugin.Plugin
	Impl StepPlugin
}

func (p *HTTPStepPlugin) Client(b *plugin.MuxBroker, client *rpc.Client) (interface{}, error) {
	return &RPCClient{client: client}, nil
}

func (p *HTTPStepPlugin) Server(*plugin.MuxBroker) (interface{}, error) {
	return &RPCServer{Impl: p.Impl}, nil
}

// RPCClient is an implementation of StepPlugin that communicates over RPC.
type RPCClient struct {
	client *rpc.Client
}

func (m *RPCClient) Run(ctx context.Context, rawInput json.RawMessage) (json.RawMessage, error) {
	var resp json.RawMessage
	err := m.client.Call("Plugin.Run", rawInput, &resp)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// RPCServer is the RPC server that RPCCLIENT talks to, conforming to
// the requirements of net/rpc
type RPCServer struct {
	Impl StepPlugin
}

func (m *RPCServer) Run(rawInput json.RawMessage, resp *json.RawMessage) error {
	result, err := m.Impl.Run(context.Background(), rawInput)
	if err != nil {
		return err
	}
	*resp = result
	return nil
}

// ---- Main Entrypoint ----
func main() {
	// Set up logging to stderr with timestamps
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.SetOutput(os.Stderr)

	// Log startup information
	log.Printf("Starting plugin with handshake config: %+v", handshake)

	// Create plugin server
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: handshake,
		Plugins: map[string]plugin.Plugin{
			"step": &HTTPStepPlugin{Impl: &HTTPPlugin{}},
		},
		Logger: hclog.New(&hclog.LoggerOptions{
			Name:   "plugin",
			Output: os.Stderr,
			Level:  hclog.Debug,
		}),
	})
}
