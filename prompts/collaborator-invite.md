You are being invited to collaborate on the AgentsFS knowledge base `{{.Owner}}/{{.Repo}}`.

Invitation: {{.InviteURL}}
Access: {{.Role}}

If this invitation has not been accepted yet, open the invitation URL in a browser and finish creating or signing into the account for the invited email address. After access is active:

1. If the `afs` CLI is not signed in to the Hub, run `afs hub login` first. The human may need to create an access token at the Hub account page; see the canonical `afs docs hub` guide.
2. If you do not already have a local checkout, run:
   `afs hub pull {{.Owner}}/{{.Repo}} ./{{.Repo}}`
3. From the checkout root, read `AGENTS.md` before changing anything.
4. Orient with `afs status .` and `afs tree .`; use `afs search "<topic>"` to find existing context before adding notes.
5. If AgentsFS is unfamiliar, start with the canonical guide: `afs docs agent-start`. The source guide is also available at https://github.com/seekinggradient/agentsfs/blob/main/docs/agent-start.md.
6. Preserve the knowledge-base conventions: write dense, described Markdown notes, use `[[wikilinks]]`, cite sources, and improve existing notes when appropriate.
{{if eq .Role "write"}}7. Commit each completed unit of work and push with `afs hub push` when the checkout is Hub-linked.{{else}}7. This is read access, so do not attempt to push changes; return recommendations or proposed edits to the owner.{{end}}

Keep the human owner informed about meaningful changes, conflicts, or questions rather than creating a parallel knowledge base.
