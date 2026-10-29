# Issue tracker: GitHub

Issues and specs for this repo live in GitHub Issues. Use `gh` from this repository.

## Conventions

- Create: `gh issue create --title "..." --body "..."`
- Read: `gh issue view <number> --json number,title,body,labels,comments`
- List: `gh issue list --state open --json number,title,body,labels,comments`
- Comment: `gh issue comment <number> --body "..."`
- Apply or remove labels: `gh issue edit <number> --add-label "..."` or `--remove-label "..."`
- Close: `gh issue close <number> --comment "..."`
- Link a child issue to its parent with GitHub sub-issues when available; otherwise put `Part of #<parent>` in the child body.

## Pull requests as a triage surface

**PRs as a request surface: no.**

## Skill instructions

- “Publish to the issue tracker” means create a GitHub issue.
- “Fetch the relevant ticket” means read its GitHub issue, including comments and labels.

## Wayfinding

A map is an issue labelled `wayfinder:map`. Its tickets are child issues labelled `wayfinder:research`, `wayfinder:prototype`, `wayfinder:grilling`, or `wayfinder:task`. Use GitHub issue dependencies for blockers when available; otherwise record `Blocked by: #<number>` in the ticket body. An unassigned ticket with no open blockers is available to claim.
