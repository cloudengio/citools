#!/usr/bin/env bash
set -euo pipefail

RUNNER_HOME=/Users/admin/actions-runner

echo "==> Installing job-started hook script"
cat << 'EOF' > "${RUNNER_HOME}/job-started.sh"
#!/usr/bin/env bash
set -eo pipefail

echo "=== ASSIGNED GITHUB WORKFLOW JOB ==="
echo "Repository:  $GITHUB_REPOSITORY"
echo "Workflow:    $GITHUB_WORKFLOW"
echo "Run ID:      $GITHUB_RUN_ID"
echo "Job:         $GITHUB_JOB"
echo "Actor:       $GITHUB_ACTOR"
echo "===================================="

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT_FILE="${RUNNER_DIR:-$SCRIPT_DIR}/job-started.json"

if command -v python3 >/dev/null 2>&1; then
    python3 -c '
import import json, os, sys

output_path = sys.argv[1]
keys = [
   "GITHUB_RUN_ID",
   "GITHUB_RUN_NUMBER",
   "GITHUB_RUN_ATTEMPT",
   "GITHUB_JOB",
   "GITHUB_WORKFLOW",
   "GITHUB_REPOSITORY",
   "GITHUB_REPOSITORY_OWNER",
   "GITHUB_EVENT_NAME",
   "GITHUB_SHA",
   "GITHUB_REF",
   "GITHUB_ACTOR",
   "RUNNER_NAME",
 ]
data = {k: os.environ.get(k, "") for k in keys if os.environ.get(k)}
with open(output_path, "w", encoding="utf-8") as f:
    json.dump(data, f, indent=2)
os.chmod(output_path, 0o600)
print(f"Saved job started info to {output_path}")
' "$OUTPUT_FILE"
fi
EOF

chmod +x "${RUNNER_HOME}/job-started.sh"

echo "==> Configuring .env with ACTIONS_RUNNER_HOOK_JOB_STARTED"
echo "ACTIONS_RUNNER_HOOK_JOB_STARTED=${RUNNER_HOME}/job-started.sh" >> "${RUNNER_HOME}/.env"

# Fix ownership — everything should belong to admin.
chown -R admin:staff "$RUNNER_HOME"

echo "==> GitHub Actions runner code installed at ${RUNNER_HOME}"
echo "==> Configuration and startup will be handled by the orchestrator via SSH."
