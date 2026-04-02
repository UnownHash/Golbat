# AGENTS.md

## Source of Truth

This project uses `CLAUDE.md` as the primary instruction file.

- Always read and follow instructions in `CLAUDE.md` before taking any action.
- Treat `CLAUDE.md` as the single source of truth for:
  - Coding conventions
  - Project structure
  - Commands (build, test, dev)
  - Architecture decisions
  - Tooling and dependencies

## Behavior Rules

- Do not duplicate or reinterpret rules from `CLAUDE.md`.
- If there is any conflict, **`CLAUDE.md` overrides this file**.
- If `CLAUDE.md` is missing or unclear, ask for clarification before proceeding.

## Fallback

If `CLAUDE.md` is not present:
- Proceed conservatively
- Follow standard best practices
- Minimize assumptions
