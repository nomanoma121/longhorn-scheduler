#!/usr/bin/env bash
# PreToolUse hook: block anything that could send work to naver/longhorn-scheduler.
# Exit 2 = block the tool call and show stderr to Claude.
set -euo pipefail

cmd=$(jq -r '.tool_input.command // empty')
[ -z "$cmd" ] && exit 0

deny() {
  echo "BLOCKED by .claude/hooks/block-upstream.sh: $1" >&2
  echo "This fork never contributes to naver/longhorn-scheduler. See AGENTS.md." >&2
  exit 2
}

# Check each simple command separately (split on ; & | and newlines) so that
# words inside e.g. a commit message don't trigger false positives.
while IFS= read -r seg; do
  # 1. Pushing to the upstream remote / naver URL
  if grep -Eq '^[[:space:]]*git([[:space:]]+-[^[:space:]]+)*[[:space:]]+push\b' <<<"$seg" &&
     grep -Eq '\bupstream\b|naver/' <<<"$seg"; then
    deny "git push to upstream"
  fi

  # 2. gh pr create must explicitly target the fork (fork default base = parent repo!)
  if grep -Eq '^[[:space:]]*gh[[:space:]]+pr[[:space:]]+create\b' <<<"$seg" &&
     ! grep -Eq '(--repo[= ]|-R[[:space:]]+)nomanoma121/longhorn-scheduler\b' <<<"$seg"; then
    deny "gh pr create without '--repo nomanoma121/longhorn-scheduler'"
  fi

  # 3. Any write-ish gh command that mentions naver
  if grep -Eq '^[[:space:]]*gh[[:space:]]' <<<"$seg" && grep -Eiq '\bnaver\b' <<<"$seg"; then
    if grep -Eq '\b(create|comment|edit|merge|close|reopen|review|ready|delete|lock|transfer)\b' <<<"$seg"; then
      deny "gh write operation referencing naver"
    fi
    if grep -Eq '^[[:space:]]*gh[[:space:]]+api\b' <<<"$seg" &&
       grep -Eq '(-X|--method)[= ]*(POST|PUT|PATCH|DELETE)|(^|[[:space:]])(-f|-F|--field|--raw-field|--input)([[:space:]=]|$)' <<<"$seg"; then
      deny "gh api write request referencing naver"
    fi
  fi
done < <(tr ';&|' '\n\n\n' <<<"$cmd")

exit 0
