package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"net/rpc"

	"github.com/hashicorp/go-plugin"
)

// HandshakeConfig is used to just do a basic handshake between
// a plugin and host. If the handshake fails, a user friendly error is shown.
// This prevents users from executing bad plugins or executing a plugin
// directory. It is a UX feature, not a security feature.
var handshakeConfig = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "ARGO_ROLLOUTS_PLUGIN",
	MagicCookieValue: "step",
}

// pluginMap is the map of plugins we can dispense.
var pluginMap = map[string]plugin.Plugin{
	"step": &HTTPStepPlugin{},
}

func main() {
	// Build the plugin binary
	cmd := exec.Command("go", "build", "-o", "curl-plugin", "..")
	cmd.Dir = "test"
	if err := cmd.Run(); err != nil {
		log.Fatalf("Failed to build plugin: %v", err)
	}

	// Get the absolute path to the plugin binary
	pluginPath, err := filepath.Abs("curl-plugin")
	if err != nil {
		log.Fatalf("Failed to get absolute path: %v", err)
	}

	// Create a new plugin client
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: handshakeConfig,
		Plugins:         pluginMap,
		Cmd:             exec.Command(pluginPath),
		AllowedProtocols: []plugin.Protocol{
			plugin.ProtocolGRPC,
			plugin.ProtocolNetRPC,
		},
	})
	defer client.Kill()

	// Connect via RPC
	rpcClient, err := client.Client()
	if err != nil {
		log.Fatal(err)
	}

	// Request the plugin
	raw, err := rpcClient.Dispense("step")
	if err != nil {
		log.Fatal(err)
	}

	// We should have a StepPlugin now! This feels like a normal interface
	// implementation but is in fact an RPC client.
	stepPlugin := raw.(StepPlugin)

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create input for the plugin
	input := PluginInput{
		Config: map[string]string{
			"uri":    "https://httpbin.org/get",
			"method": "GET",
		},
	}

	// Marshal the input to JSON
	inputJSON, err := json.Marshal(input)
	if err != nil {
		log.Fatal(err)
	}

	// Call the plugin
	result, err := stepPlugin.Run(ctx, inputJSON)
	if err != nil {
		log.Fatal(err)
	}

	// Unmarshal the result
	var output PluginOutput
	if err := json.Unmarshal(result, &output); err != nil {
		log.Fatal(err)
	}

	// Print the result
	fmt.Printf("Success: %v\n", output.Success)
	fmt.Printf("Message: %s\n", output.Message)

	// Clean up the plugin binary
	if err := os.Remove(pluginPath); err != nil {
		log.Printf("Warning: Failed to remove plugin binary: %v", err)
	}
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
	*resp, _ = m.Impl.Run(context.Background(), rawInput)
	return nil
}
