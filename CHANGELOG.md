# Changelog

## Unreleased

- Fix root CLI metadata so the installed command identifies itself as `norma`.
- Remove tracked local binaries and machine-specific agent configuration.
- Add public repository hygiene docs and security scanning workflow.

## v2.0.0

- Move the module path to `github.com/normahq/norma/v2`.
- Move reusable runtime packages to `github.com/normahq/runtime/v2`.
- Remove embedded protocol tools now distributed as standalone repositories:
  `acp-dump`, `mcp-dump`, and `acp-repl`.
- Use short provider IDs in generated config and docs, with `opencode` as the
  default provider.
