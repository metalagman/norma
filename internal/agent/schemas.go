package agent

const (
	inputSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "run": {
      "type": "object",
      "properties": {
        "id": { "type": "string" },
        "iteration": { "type": "integer" }
      },
      "required": ["id", "iteration"]
    },
    "task": {
      "type": "object",
      "properties": {
        "id": { "type": "string" },
        "title": { "type": "string" },
        "description": { "type": "string" },
        "acceptance_criteria": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "id": { "type": "string" },
              "text": { "type": "string" },
              "verify_hints": { "type": "array", "items": { "type": "string" } }
            },
            "required": ["id", "text"]
          }
        }
      },
      "required": ["id", "title", "description", "acceptance_criteria"]
    },
    "step": {
      "type": "object",
      "properties": {
        "index": { "type": "integer" },
        "name": { "type": "string" },
        "dir": { "type": "string" }
      },
      "required": ["index", "name", "dir"]
    },
    "paths": {
      "type": "object",
      "properties": {
        "workspace_dir": { "type": "string" },
        "workspace_mode": { "type": "string" },
        "run_dir": { "type": "string" },
        "code_root": { "type": "string" }
      },
      "required": ["workspace_dir", "workspace_mode", "run_dir", "code_root"]
    }
  },
  "required": ["run", "task", "step", "paths"]
}`

	outputSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "status": { "type": "string", "enum": ["ok", "stop", "error"] },
    "stop_reason": { "type": "string" },
    "summary": {
      "type": "object",
      "properties": {
        "text": { "type": "string" },
        "warnings": { "type": "array", "items": { "type": "string" } },
        "errors": { "type": "array", "items": { "type": "string" } }
      },
      "required": ["text"]
    },
    "progress": {
      "type": "object",
      "properties": {
        "title": { "type": "string" },
        "details": { "type": "array", "items": { "type": "string" } }
      },
      "required": ["title", "details"]
    }
  },
  "required": ["status", "summary", "progress"]
}`
)
