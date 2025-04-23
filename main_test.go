package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/go-plugin"
)

func TestPluginIntegration(t *testing.T) {
	// Build the plugin binary
	cmd := exec.Command("go", "build", "-o", "curl-plugin")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to build plugin: %v", err)
	}
	defer os.Remove("curl-plugin") // Clean up after test

	// Get the absolute path to the plugin binary
	pluginPath, err := filepath.Abs("curl-plugin")
	if err != nil {
		t.Fatalf("Failed to get absolute path: %v", err)
	}

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

	// Test cases
	tests := []struct {
		name    string
		input   PluginInput
		wantErr bool
	}{
		{
			name: "valid request",
			input: PluginInput{
				Config: map[string]string{
					"uri":    "https://ifconfig.me",
					"method": "GET",
				},
			},
			wantErr: false,
		},
		{
			name: "missing uri",
			input: PluginInput{
				Config: map[string]string{
					"method": "GET",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal the input to JSON
			inputJSON, err := json.Marshal(tt.input)
			if err != nil {
				t.Fatalf("Failed to marshal input: %v", err)
			}

			// Call the plugin
			result, err := stepPlugin.Run(ctx, inputJSON)
			if tt.wantErr {
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

			if !output.Success {
				t.Errorf("Expected successful response, got: %v", output.Message)
			}
		})
	}
}
