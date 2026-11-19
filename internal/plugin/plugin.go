package plugin

import (
	"context"
	"fmt"
)

// Field is one input a resource type collects.
// Secret fields are encrypted and are never returned to the browser.
type Field struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Secret bool   `json:"secret"`
}

// Info is the public description of a resource type.
// ToolDescription says what the tool does. CommandDescription says which
// commands that type accepts and how to write one the user can read.
// Neither field mentions approval. The MCP instructions cover that.
type Info struct {
	Type               string  `json:"type"`
	Label              string  `json:"label"`
	Fields             []Field `json:"fields"`
	ToolDescription    string  `json:"-"`
	CommandDescription string  `json:"-"`
}

// Plugin is one resource type. A new type is a package that implements this
// and is added to the registry.
type Plugin interface {
	Info() Info
	Test(ctx context.Context, config, secrets map[string]string) error
	// Execute runs one approved command. The result is returned to the harness.
	// Stop when ctx is cancelled. An error means the command failed.
	Execute(ctx context.Context, command string, config, secrets map[string]string) (any, error)
}

// Registry is the list of resource types the application knows.
type Registry struct {
	list   []Plugin
	byType map[string]Plugin
}

func New(plugins ...Plugin) (*Registry, error) {
	registry := &Registry{byType: make(map[string]Plugin, len(plugins))}
	for _, item := range plugins {
		info := item.Info()
		if info.Type == "" || info.Label == "" {
			return nil, fmt.Errorf("plugin type and label are required")
		}
		if info.ToolDescription == "" || info.CommandDescription == "" {
			return nil, fmt.Errorf("plugin %s needs tool and command descriptions", info.Type)
		}
		if _, ok := registry.byType[info.Type]; ok {
			return nil, fmt.Errorf("duplicate plugin %s", info.Type)
		}
		registry.byType[info.Type] = item
		registry.list = append(registry.list, item)
	}
	return registry, nil
}

func (r *Registry) List() []Info {
	out := make([]Info, 0, len(r.list))
	for _, item := range r.list {
		out = append(out, item.Info())
	}
	return out
}

func (r *Registry) Get(resourceType string) (Plugin, bool) {
	item, ok := r.byType[resourceType]
	return item, ok
}
