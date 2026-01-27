package agent

func getInputSchema(role string) string {
	return commonInputSchema
}

func getOutputSchema(role string) string {
	switch role {
	case rolePlan:
		return planOutputSchema
	case roleDo:
		return doOutputSchema
	case roleCheck:
		return checkOutputSchema
	case roleAct:
		return actOutputSchema
	default:
		return commonOutputSchema
	}
}

const commonInputSchema = `{
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
    },
    "plan": { "type": "object" },
    "do": { "type": "object" },
    "check": { "type": "object" }
  },
  "required": ["run", "task", "step", "paths"]
}`

const commonOutputSchema = `{
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

const planOutputSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "status": { "type": "string", "enum": ["ok", "stop", "error"] },
    "stop_reason": { "type": "string" },
    "summary": {
      "type": "object",
      "properties": {
        "text": { "type": "string" }
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
    },
    "plan": {
      "type": "object",
      "properties": {
        "task_id": { "type": "string" },
        "goal": { "type": "string" },
        "constraints": { "type": "array", "items": { "type": "string" } },
        "acceptance_criteria": {
          "type": "object",
          "properties": {
            "baseline": { "type": "array" },
            "effective": { "type": "array" }
          },
          "required": ["baseline", "effective"]
        },
        "work_plan": {
          "type": "object",
          "properties": {
            "timebox_minutes": { "type": "integer" },
            "do_steps": { "type": "array" },
            "check_steps": { "type": "array" },
            "stop_triggers": { "type": "array" }
          },
          "required": ["timebox_minutes", "do_steps", "check_steps", "stop_triggers"]
        }
      },
      "required": ["task_id", "goal", "acceptance_criteria", "work_plan"]
    }
  },
  "required": ["status", "summary", "progress", "plan"]
}`

const doOutputSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "status": { "type": "string", "enum": ["ok", "stop", "error"] },
    "stop_reason": { "type": "string" },
    "summary": {
      "type": "object",
      "properties": {
        "text": { "type": "string" }
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
    },
    "do": {
      "type": "object",
      "properties": {
        "execution": {
          "type": "object",
          "properties": {
            "executed_step_ids": { "type": "array", "items": { "type": "string" } },
            "skipped_step_ids": { "type": "array", "items": { "type": "string" } },
            "commands": { "type": "array" }
          },
          "required": ["executed_step_ids", "skipped_step_ids", "commands"]
        },
        "blockers": { "type": "array" }
      },
      "required": ["execution"]
    }
  },
  "required": ["status", "summary", "progress", "do"]
}`

const checkOutputSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "status": { "type": "string", "enum": ["ok", "stop", "error"] },
    "stop_reason": { "type": "string" },
    "summary": {
      "type": "object",
      "properties": {
        "text": { "type": "string" }
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
    },
    "check": {
      "type": "object",
      "properties": {
        "plan_match": { "type": "object" },
        "acceptance_results": { "type": "array" },
        "verdict": {
          "type": "object",
          "properties": {
            "status": { "type": "string", "enum": ["PASS", "FAIL", "PARTIAL"] },
            "recommendation": { "type": "string" },
            "basis": { "type": "object" }
          },
          "required": ["status", "recommendation", "basis"]
        }
      },
      "required": ["plan_match", "acceptance_results", "verdict"]
    }
  },
  "required": ["status", "summary", "progress", "check"]
}`

const actOutputSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "status": { "type": "string", "enum": ["ok", "stop", "error"] },
    "stop_reason": { "type": "string" },
    "summary": {
      "type": "object",
      "properties": {
        "text": { "type": "string" }
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
    },
    "act": {
      "type": "object",
      "properties": {
        "decision": { "type": "string", "enum": ["close", "replan", "rollback", "continue"] },
        "rationale": { "type": "string" },
        "next": { "type": "object" }
      },
      "required": ["decision", "rationale"]
    }
  },
  "required": ["status", "summary", "progress", "act"]
}`