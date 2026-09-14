# Maintenance

## Branch model

`personal/main` is the maintained personal branch. `upstream` points to `https://github.com/router-for-me/CLIProxyAPI.git`; `origin` points to this public personal repository. Preserve upstream history and attribution.

## Backport process

1. Start with a clean candidate branch or isolated worktree and record the current personal revision.
2. Fetch upstream refs without merging. Compare the last reviewed upstream revision with the desired tag, including non-merge commits and dependencies.
3. Classify every candidate as included, adapted, excluded or deferred. Keep original SHAs and reasons in `UPSTREAM_PATCHES.md`.
4. Apply self-contained fixes with `git cherry-pick -x`. For mixed commits, preserve the source SHA and explain the retained portion. A clean textual merge is not proof of correct behavior.
5. Keep Devin and the dynamic plugin framework absent. Preserve `CUSTOMIZATIONS.md`, native fallback behavior, and the original regression tests. Do not add switches or stubs for removed integrations.
6. Run focused tests followed by the required build and suite gates. Recheck applicable native identity, routing, proxy, authentication and streaming behavior. Use local mock requests for reproducible smoke tests.
7. Review the final stable target. Complex changes to authentication, concurrency or coupled request paths require an independent review after main review and required tests pass.
8. Commit exact reviewed paths, verify the staged diff, then push the reviewed branch. Publishing code, creating releases, and deploying a running service are separate operations.

Use a personal version label or the Git revision. Always distinguish the upstream base version from the subset of later commits reviewed and adopted.

## Public repository hygiene

- Preserve `LICENSE` and upstream attribution.
- Do not commit runtime configuration, account/OAuth files, API tokens, logs or local deployment evidence.
- Keep CI limited to tests and builds unless a separate release or deployment workflow is intentionally introduced.
- Do not carry upstream sponsor promotions, organization-specific PR automation or upstream container publishing destinations into the personal default workflow.
- Record actual executed checks and explicit integration-test gaps. Do not infer live provider health from unit tests or mock traffic.
