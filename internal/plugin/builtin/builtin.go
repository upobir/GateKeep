// Package builtin is the backend registry. Add a resource type by importing
// its package and passing it to plugin.New.
package builtin

import (
	"gatekeep/internal/plugin"
	"gatekeep/internal/plugin/mock"
)

func New() (*plugin.Registry, error) {
	return plugin.New(mock.Plugin{})
}
