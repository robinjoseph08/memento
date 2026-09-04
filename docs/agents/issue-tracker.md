# Issue tracker: GitHub

Issues and specifications for this repository live in GitHub Issues at `robinjoseph08/memento`. Use the `gh` CLI for all operations.

## Conventions

- Create, read, update, label, comment on, and close issues with `gh issue`.
- Infer the repository from the current Git remote.
- Use multi-line bodies rather than compressing issue content into titles or comments.
- Pull requests are not part of the triage request queue.

## Dependencies

Use GitHub's native issue dependency API for every blocking relationship. Textual `Blocked by` sections may supplement native relationships but never replace them.

Create a dependency with:

`POST /repos/robinjoseph08/memento/issues/{issue_number}/dependencies/blocked_by`

Pass the blocker's numeric database ID as `issue_id`. Verify every relationship with the corresponding `GET` endpoint.

## Skill behavior

- When a skill says to publish a specification, create a GitHub issue.
- When a skill says to publish tickets, create GitHub issues in dependency order.
- When a skill asks for a ticket, fetch its body, labels, and comments.
- Apply labels according to `docs/agents/triage-labels.md`.
