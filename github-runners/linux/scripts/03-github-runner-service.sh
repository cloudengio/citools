#!/usr/bin/env bash
set -euo pipefail

RUNNER_HOME=/home/admin/actions-runner

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
import os, json, sys

output_path = sys.argv[1]
with open(output_path, "w", encoding="utf-8") as f:
    json.dump(dict(os.environ), f, indent=2)
print(f"Saved job started info to {output_path}")
' "$OUTPUT_FILE"
fi
EOF

chmod +x "${RUNNER_HOME}/job-started.sh"

echo "==> Configuring .env with ACTIONS_RUNNER_HOOK_JOB_STARTED"
echo "ACTIONS_RUNNER_HOOK_JOB_STARTED=${RUNNER_HOME}/job-started.sh" >> "${RUNNER_HOME}/.env"

# Fix ownership — everything should belong to admin.
sudo chown -R admin:admin "$RUNNER_HOME"

echo "==> GitHub Actions runner code installed at ${RUNNER_HOME}"
echo "==> Configuration and startup will be handled by the orchestrator via SSH."
