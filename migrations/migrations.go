// Package migrations 内嵌数据库迁移脚本，供服务启动时应用；
// 同一目录下的 .sql 文件也可被 sqlite3 命令行直接使用（见 Makefile）。
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
