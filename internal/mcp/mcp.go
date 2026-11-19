// Package mcp is the Model Context Protocol surface for resource tools.
// A resource type is exposed only when at least one resource of that type is saved.
// A call does not run the command; it asks a person to approve it.
package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"gatekeep/internal/plugin"
)

const (
	// ModernVersion is the per-request protocol revision this server speaks.
	ModernVersion = "2026-07-28"
	// PreferredLegacy is the initialize handshake revision offered when a
	// client asks for a legacy version this server does not implement.
	PreferredLegacy = "2025-11-25"

	ServerName    = "GateKeep"
	ServerVersion = "0.1.0"

	CodeParseError         = -32700
	CodeInvalidRequest     = -32600
	CodeMethodNotFound     = -32601
	CodeInvalidParams      = -32602
	CodeInternal           = -32603
	CodeHeaderMismatch     = -32020
	CodeUnsupportedVersion = -32022
)

// Instructions is the guidance sent to the model.
// Later, mention execution timeouts and the approval timeout once those settings exist.
func Instructions(appURL string) string {
	return fmt.Sprintf("Every tool call waits for a person to approve it in GateKeep. The command does not run until then. Write the command so a person can read it, because a person checks it before approving. Call the tool for the resource type and pass one of that tool's resource names, the command, and a one-line reason. GateKeep is at %s. Give the user that link when they need to approve a request. The tool response also includes a link to the specific request. Do not claim the command ran.", appURL)
}

// LegacyVersions are the initialize-handshake revisions this server accepts.
var LegacyVersions = map[string]bool{
	"2025-03-26": true,
	"2025-06-18": true,
	"2025-11-25": true,
}

// Tool is one MCP tool, built from a resource plugin.
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Call is a tools/call invocation after its arguments have been checked.
type Call struct {
	Name     string
	Resource string
	Command  string
	Reason   string
}

// Reject means the model sent arguments that cannot be queued.
// The text is returned as a tool error so the model can correct the call.
type Reject struct {
	Text string
}

func (e *Reject) Error() string {
	return e.Text
}

// Tools builds one tool for each resource type that has at least one saved resource.
// namesByType maps a resource type to the resource names the model may pass.
// Types keep registry order. Names keep the order they are given.
func Tools(infos []plugin.Info, namesByType map[string][]string) []Tool {
	tools := make([]Tool, 0)
	for _, info := range infos {
		names := namesByType[info.Type]
		if len(names) == 0 {
			continue
		}
		tools = append(tools, Tool{
			Name:        info.Type,
			Title:       info.Label,
			Description: info.ToolDescription,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"resource": map[string]any{
						"type":        "string",
						"enum":        names,
						"description": "Name of a configured " + info.Label + " resource.",
					},
					"command": map[string]any{
						"type":        "string",
						"description": info.CommandDescription,
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "One line explaining why this command should run.",
					},
				},
				"required":             []string{"resource", "command", "reason"},
				"additionalProperties": false,
			},
		})
	}
	return tools
}

// DecodeCall reads a tools/call params object.
func DecodeCall(params json.RawMessage) (Call, error) {
	if len(params) == 0 || string(params) == "null" {
		return Call{}, fmt.Errorf("tools/call params are required")
	}
	var body struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &body); err != nil {
		return Call{}, fmt.Errorf("tools/call params are invalid")
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return Call{}, fmt.Errorf("tool name is required")
	}
	if len(body.Arguments) == 0 || string(body.Arguments) == "null" {
		return Call{}, fmt.Errorf("tool arguments are required")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body.Arguments, &raw); err != nil {
		return Call{}, fmt.Errorf("tool arguments must be an object")
	}
	resource, err := jsonString(raw["resource"])
	if err != nil {
		return Call{}, &Reject{Text: "resource must be a string"}
	}
	command, err := jsonString(raw["command"])
	if err != nil {
		return Call{}, &Reject{Text: "command must be a string"}
	}
	reason, err := jsonString(raw["reason"])
	if err != nil {
		return Call{}, &Reject{Text: "reason must be a string"}
	}
	call := Call{
		Name:     name,
		Resource: strings.TrimSpace(resource),
		Command:  strings.TrimSpace(command),
		Reason:   strings.TrimSpace(reason),
	}
	if call.Resource == "" {
		return Call{}, &Reject{Text: "resource is required"}
	}
	if call.Command == "" {
		return Call{}, &Reject{Text: "command is required"}
	}
	if call.Reason == "" {
		return Call{}, &Reject{Text: "reason is required"}
	}
	return call, nil
}

func jsonString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

// ClientName reads the harness name from a modern _meta block.
func ClientName(params json.RawMessage) string {
	if len(params) == 0 || string(params) == "null" {
		return ""
	}
	var body struct {
		Meta struct {
			ClientInfo struct {
				Name string `json:"name"`
			} `json:"io.modelcontextprotocol/clientInfo"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(params, &body); err != nil {
		return ""
	}
	return strings.TrimSpace(body.Meta.ClientInfo.Name)
}

// ProtocolVersion reads io.modelcontextprotocol/protocolVersion from _meta.
func ProtocolVersion(params json.RawMessage) string {
	if len(params) == 0 || string(params) == "null" {
		return ""
	}
	var body struct {
		Meta struct {
			ProtocolVersion string `json:"io.modelcontextprotocol/protocolVersion"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(params, &body); err != nil {
		return ""
	}
	return body.Meta.ProtocolVersion
}

// ParamName reads params.name, used to check the Mcp-Name header.
func ParamName(params json.RawMessage) (string, bool) {
	if len(params) == 0 || string(params) == "null" {
		return "", false
	}
	var body struct {
		Name *string `json:"name"`
	}
	if err := json.Unmarshal(params, &body); err != nil || body.Name == nil {
		return "", false
	}
	return *body.Name, true
}

// QueuedText is the tool result while a person still has to approve the command.
func QueuedText(resourceName, approvalURL string) string {
	return fmt.Sprintf(
		"Queued for human approval. This command has not executed.\nResource: %s\nApproval: %s\nA person must approve or reject it in the GateKeep web UI before it can run.",
		resourceName,
		approvalURL,
	)
}

// HarnessName keeps a client name safe to store and show.
func HarnessName(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\n", " "))
	if name == "" {
		return "unknown"
	}
	if len(name) > 200 {
		return name[:200]
	}
	return name
}
