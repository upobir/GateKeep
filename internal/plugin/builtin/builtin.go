// Package builtin is the backend registry. Add a resource type by importing
// its package and passing it to plugin.New.
package builtin

import (
	"gatekeep/internal/plugin"
	"gatekeep/internal/plugin/clickhouse"
	"gatekeep/internal/plugin/mock"
	"gatekeep/internal/plugin/mongodb"
	"gatekeep/internal/plugin/mysql"
	"gatekeep/internal/plugin/postgres"
	"gatekeep/internal/plugin/redis"
)

func New() (*plugin.Registry, error) {
	return plugin.New(mock.Plugin{}, mysql.Plugin{}, postgres.Plugin{}, clickhouse.Plugin{}, mongodb.Plugin{}, redis.Plugin{})
}
