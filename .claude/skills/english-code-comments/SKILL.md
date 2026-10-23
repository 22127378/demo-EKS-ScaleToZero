---
name: english-code-comments
description: Write every code comment in English. Use whenever you create or edit code or config files (Go, TypeScript/React, shell, Terraform, Helm templates, YAML, Dockerfile, Makefile) and whenever you touch a file that already has non-English comments.
---

# English-only code comments

All comments in this repository's code must be written in English, even when the conversation with the user is in Vietnamese.

## Rules

1. **Scope.** Applies to every comment form: `//`, `/* */`, `#`, `{{- /* */}}` (Helm/Go templates), `<!-- -->`, doc comments, and `TODO`/`FIXME`/`NOTE` markers.
2. **New code.** Write new comments in English from the start. Keep them short and explain *why*, not what the code already says.
3. **Existing code.** When you edit a file or block that contains non-English comments, translate the comments in the part you touched in the same change. Keep the meaning; do not add or drop information.
4. **References.** Keep identifiers, paths, and config keys exactly as they are. Refer to plan steps as `PLAN.md step N`, not in Vietnamese.
5. **Not covered.** This skill is about comments only. Do not change user-facing strings (error messages, log lines, CLI output, chart `description`) or documentation files such as `PLAN.md` and `README.md` unless the user asks.

## Check before finishing

Run this from the repository root and make sure no comment lines are listed. Lines that only contain non-English text inside a quoted string are expected and fine.

```bash
grep -rnP '^\s*(#|//|\*|/\*|\{\{-?\s*/\*).*[^\x00-\x7F]|\s#\s.*[^\x00-\x7F]' \
  --exclude-dir=.git --exclude-dir=node_modules --exclude=PLAN.md --exclude=README.md .
```
