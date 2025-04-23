package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
)

func TestPluginExecution(t *testing.T) {
	// Build the plugin binary
	cmd := exec.Command("go", "build", "-o", "curl-plugin")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to build plugin: %v", err)
	}
	defer os.Remove("curl-plugin")

	// Get the absolute path to the plugin binary
	pluginPath, err := filepath.Abs("curl-plugin")
	if err != nil {
		t.Fatalf("Failed to get absolute path: %v", err)
	}

	// Make sure the binary is executable
	if err := os.Chmod(pluginPath, 0755); err != nil {
		t.Fatalf("Failed to make plugin executable: %v", err)
	}

	// Test cases
	tests := []struct {
		name        string
		input       PluginInput
		expectError bool
		checkOutput func(t *testing.T, output PluginOutput)
	}{
		{
			name: "successful http request",
			input: PluginInput{
				Config: map[string]string{
					"uri":    "https://ifconfig.me",
					"method": "GET",
				},
			},
			expectError: false,
			checkOutput: func(t *testing.T, output PluginOutput) {
				if !output.Success {
					t.Errorf("Expected successful response, got: %v", output.Message)
				}
				if output.Message == "" {
					t.Error("Expected non-empty message")
				}
			},
		},
		{
			name: "missing uri parameter",
			input: PluginInput{
				Config: map[string]string{
					"method": "GET",
				},
			},
			expectError: true,
		},
		{
			name: "invalid url",
			input: PluginInput{
				Config: map[string]string{
					"uri":    "http://invalid-url-that-does-not-exist",
					"method": "GET",
				},
			},
			expectError: false, // We expect a response with Success=false
			checkOutput: func(t *testing.T, output PluginOutput) {
				if output.Success {
					t.Error("Expected unsuccessful response for invalid URL")
				}
				if output.Message == "" {
					t.Error("Expected error message for invalid URL")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new plugin client
			client := plugin.NewClient(&plugin.ClientConfig{
				HandshakeConfig: handshake,
				Plugins: map[string]plugin.Plugin{
					"step": &HTTPStepPlugin{},
				},
				Cmd: exec.Command(pluginPath),
				AllowedProtocols: []plugin.Protocol{
					plugin.ProtocolGRPC,
					plugin.ProtocolNetRPC,
				},
			})
			defer client.Kill()

			// Connect via RPC
			rpcClient, err := client.Client()
			if err != nil {
				t.Fatalf("Failed to connect to plugin: %v", err)
			}

			// Request the plugin
			raw, err := rpcClient.Dispense("step")
			if err != nil {
				t.Fatalf("Failed to dispense plugin: %v", err)
			}

			// We should have a StepPlugin now!
			stepPlugin := raw.(StepPlugin)

			// Create a context with timeout
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Marshal the input to JSON
			inputJSON, err := json.Marshal(tt.input)
			if err != nil {
				t.Fatalf("Failed to marshal input: %v", err)
			}

			// Call the plugin
			result, err := stepPlugin.Run(ctx, inputJSON)
			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Unmarshal the result
			var output PluginOutput
			if err := json.Unmarshal(result, &output); err != nil {
				t.Fatalf("Failed to unmarshal output: %v", err)
			}

			// Check the output
			if tt.checkOutput != nil {
				tt.checkOutput(t, output)
			}
		})
	}
}

// TestArgoRolloutsEnvironment simulates how Argo Rollouts loads and executes the plugin
func TestArgoRolloutsEnvironment(t *testing.T) {
	// Build the plugin binary with the same settings as Argo Rollouts
	cmd := exec.Command("go", "build",
		"-o", "curl-plugin",
		"-ldflags", "-s -w",
		"-tags", "netgo",
		"-installsuffix", "netgo",
	)

	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to build plugin: %v", err)
	}
	defer os.Remove("curl-plugin")

	// Get the absolute path to the plugin binary
	pluginPath, err := filepath.Abs("curl-plugin")
	if err != nil {
		t.Fatalf("Failed to get absolute path: %v", err)
	}

	// Set permissions to match Argo Rollouts environment
	if err := os.Chmod(pluginPath, 0700); err != nil {
		t.Fatalf("Failed to set permissions: %v", err)
	}

	// Create a logger that writes to the test output
	logger := hclog.New(&hclog.LoggerOptions{
		Name:   "plugin-test",
		Output: &testOutput{t: t},
		Level:  hclog.Trace,
	})

	// Create a new plugin client with detailed logging
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: handshake,
		Plugins: map[string]plugin.Plugin{
			"step": &HTTPStepPlugin{},
		},
		Cmd: exec.Command(pluginPath),
		AllowedProtocols: []plugin.Protocol{
			plugin.ProtocolGRPC,
			plugin.ProtocolNetRPC,
		},
		Logger: logger,
	})
	defer client.Kill()

	// Connect via RPC
	rpcClient, err := client.Client()
	if err != nil {
		t.Fatalf("Failed to connect to plugin: %v", err)
	}

	// Request the plugin
	raw, err := rpcClient.Dispense("step")
	if err != nil {
		t.Fatalf("Failed to dispense plugin: %v", err)
	}

	// We should have a StepPlugin now!
	stepPlugin := raw.(StepPlugin)

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test a simple request
	input := PluginInput{
		Config: map[string]string{
			"uri":    "https://ifconfig.me",
			"method": "GET",
		},
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("Failed to marshal input: %v", err)
	}

	result, err := stepPlugin.Run(ctx, inputJSON)
	if err != nil {
		t.Fatalf("Plugin execution failed: %v", err)
	}

	var output PluginOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("Failed to unmarshal output: %v", err)
	}

	if !output.Success {
		t.Errorf("Expected successful response, got: %v", output.Message)
	}
}

// testOutput implements io.Writer to capture plugin logs
type testOutput struct {
	t *testing.T
}

func (o *testOutput) Write(p []byte) (n int, err error) {
	o.t.Log(string(p))
	return len(p), nil
}
