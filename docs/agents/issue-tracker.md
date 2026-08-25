# Issue tracker

## Canonical tracker

Use GitLab Issues for work items and lifecycle tracking.

This is an existing repository convention, established by the OpenSpec v2 workflow and the `publish-v2.sh` / `publish-change` skills:

- publishing a change creates a GitLab Issue with the `change` label;
- the Issue description carries `change_id`, `branch`, and `branch_head_sha`;
- implementation review happens through a normal GitLab merge request;
- archiving closes the linked GitLab Issue through the Bridge.

The `change` label is a workflow/publication label, not a triage label.

## Project resolution

When Git metadata is available, resolve the project from the upstream GitLab remote. If the checkout has no remote or is an exported workspace, set `SPECWIRE_GITLAB_PROJECT=<group/project>` explicitly before using a publisher or API client. Never infer or invent a project path from the directory name.

This repository currently has no local `.git` metadata, so the explicit `SPECWIRE_GITLAB_PROJECT` path is required for operations that address a GitLab project.

## Local artifacts

Do not create a parallel `.scratch/<effort>/` issue queue for normal work. Use the GitLab Issue as the system of record; local scratch material is supplemental and must not replace the linked Issue or merge request.
