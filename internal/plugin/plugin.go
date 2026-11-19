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
type Info struct {
	Type   string  `json:"type"`
	Label  string  `json:"label"`
	Fields []Field `json:"fields"`
}

// Plugin is one resource type. A new type is a package that implements this
// and is added to the registry.
type Plugin interface {
	Info() Info
	Test(ctx context.Context, config, secrets map[string]string) error
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
