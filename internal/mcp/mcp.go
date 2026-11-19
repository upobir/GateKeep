// Package mcp is the Model Context Protocol surface for resource tools.
// A resource type is exposed only when at least one resource of that type is saved.
// A call does not run the command; it asks the user to approve it.
package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
func Instructions(appURL string, approvalWait, executionLimit time.Duration) string {
	return fmt.Sprintf("Every tool call waits for the user to approve it in GateKeep. The command does not run until then. Write the command so the user can read it, because the user checks it before approving. Call the tool for the resource type and pass one of that tool's resource names, the command, and a one-line reason. A request expires if the user does not approve it within %s, and the command does not run. After approval, a command stops if it runs longer than %s. GateKeep is at %s. Give the user that link when they need to approve a request. The tool result includes a link to the specific request. Do not claim the command ran.", approvalPhrase(approvalWait), approvalPhrase(executionLimit), appURL)
}

func approvalPhrase(wait time.Duration) string {
	seconds := int(wait / time.Second)
	if seconds == 1 {
		return "1 second"
	}
	if seconds > 1 {
		return fmt.Sprintf("%d seconds", seconds)
	}
	return "300 seconds"
}

// LegacyVersions are the initialize-handshake revisions this server accepts.
var LegacyVersions = map[string]bool{
	"2025-03-26": true,
	"2025-06-18": true,
	"2025-11-25": true,
}

// Tool is one MCP tool, built from a resource plugin.
type Tool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
}

// Outcome is the tools/call result.
// ok is false when the tool call failed.
// kind tool_call_error means the command itself failed.
// user_cancelled_mid_tool_call means the user stopped it while it was running.
// tool_call_timed_out means it ran longer than its limit.
type Outcome struct {
	OK     bool   `json:"ok"`
	Kind   string `json:"kind"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// OutputSchema is the JSON Schema for Outcome.
func OutputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{
				"type":        "boolean",
				"description": "False when this tool call failed.",
			},
			"kind": map[string]any{
				"type":        "string",
				"enum":        []string{"tool_call_successful", "tool_call_error", "user_rejected", "approval_expired", "user_cancelled_mid_tool_call", "tool_call_timed_out"},
				"description": "tool_call_successful when the command ran and the user shared the result. tool_call_error when the command failed and the user shared that failure. user_rejected when the user refused the command or refused to share its result. approval_expired when the request ended before the user decided. user_cancelled_mid_tool_call when the user stopped a running command. tool_call_timed_out when the command ran longer than its limit.",
			},
			"result": map[string]any{
				"description": "Value returned by the command. Present only when ok is true.",
			},
			"error": map[string]any{
				"type":        "string",
				"description": "Why the command failed, or why the request expired.",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Why the user rejected the command, or why they cancelled it while it was running.",
			},
		},
		"required":             []string{"ok", "kind"},
		"additionalProperties": false,
	}
}

// Ran is a successful command result.
func Ran(result any) Outcome {
	return Outcome{OK: true, Kind: "tool_call_successful", Result: result}
}

// Failed is a tool call that did not produce a result.
// kind is tool_call_error when the command itself failed.
func Failed(kind, err string) Outcome {
	return Outcome{OK: false, Kind: kind, Error: err}
}

// JSON is the raw text form of an outcome.
func (o Outcome) JSON() string {
	raw, _ := json.Marshal(o)
	return string(raw)
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
			Name:         info.Type,
			Title:        info.Label,
			Description:  info.ToolDescription,
			OutputSchema: OutputSchema(),
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
