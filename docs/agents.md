# Agent Configuration

Norma supports multiple agent backends through `.norma/config.yaml`.

## OpenAI API Agent

Use `type: openai` to run PDCA roles through the OpenAI Responses API (via `openai-go`).

```yaml
agents:
  openai_primary:
    type: openai
    model: gpt-5
    api_key_env: OPENAI_API_KEY
    base_url: https://api.openai.com/v1
    timeout: 60

profiles:
  openai:
    pdca:
      plan: openai_primary
      do: openai_primary
      check: openai_primary
      act: openai_primary
```

Fields:
- `model`: Required model name (for example `gpt-5`).
- `api_key_env`: Environment variable that contains the API key.
- `api_key`: Optional inline secret (prefer `api_key_env`).
- `base_url`: Optional base URL override.
- `timeout`: Optional request timeout in seconds.

## Environment Variables

Viper env autowiring is enabled with the `NORMA_` prefix.

Examples:
- `NORMA_PROFILE=openai`
- `OPENAI_API_KEY=...`
- `NORMA_AGENTS_OPENAI_PRIMARY_TIMEOUT=90`
