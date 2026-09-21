// Package migrations 以可嵌入的形式提供全部 SQL 迁移文件，
// 服务启动时自动应用，无需外部 sqlite3 工具。
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
