---
name: gopls-mcp
description: Mandatory gopls workflow and Go workspace tools for OpenCode.
---

# gopls-mcp

## Mandatory Startup
1. `go_workspace` — Learn the Go workspace structure.
2. `go_vulncheck` — Run immediately after confirming Go workspace.

## Read Workflow
go_workspace → go_search → go_file_context → go_package_api

## Edit Workflow
Read → Find References → Edit → go_diagnostics → Fix → go_vulncheck → go test
