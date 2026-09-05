---
description: Adopted workspace as the canonical product term across AgentsFS, its hosted agent, and marketing site, with compatible tool names and a versioned contract upgrade.
---

# Workspace terminology

The owner explicitly requested a comprehensive replacement of the former product term across project code and documentation, and authorized deployment. Workspace is now the human-facing name for an AgentsFS instance. An agent’s working directory may contain several workspaces.

CLI v0.14.0 bundles contract 0.12.1. The original 0.12.0 contract and backlog spine are preserved as byte-exact upgrade baselines, alongside earlier released snapshots. Existing user repositories, slugs, routes, permissions, and content are not renamed by application deployment.

The Hub advertises `list_workspaces` and retains `list_kbs` for existing MCP clients. The hosted agent advertises `create_workspace` and `switch_workspace`, with compatibility for the former creation tool and voice focus control. Templates, onboarding, help, prompts, UI copy, permission descriptions, and maintained and archived project documentation use the current vocabulary.

Validation before release: complete Go suite; stock-contract upgrade regression; hosted-agent TypeScript, full tests, Eve build, and Next production build; marketing-site typecheck, build, and hygiene checks. Release checkouts exclude unrelated uncommitted Hub work. Production verification is recorded in the release handoff after deployment.

## Written directly

Updated the canonical vocabulary in `docs/concepts.md`, MCP compatibility in `docs/mcp.md`, and the corresponding hosted-agent README and prompts. Renamed the access-design document to `workspace-access-and-isolation.md` and repaired its references.
