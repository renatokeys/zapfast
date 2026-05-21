# Required GitHub Secrets

Configure these in the `renatokeys/zapfast` repo at:
`Settings > Secrets and variables > Actions`.

## Repository secrets

| Secret | Required by | Purpose | How to obtain |
|---|---|---|---|
| `CODECOV_TOKEN` | `ci.yml` (test-unit) | Upload coverage reports to Codecov | Sign up at https://codecov.io, add the repo, copy the upload token |
| `KUBECONFIG_PROD` | `deploy.yml` | Base64-encoded kubeconfig for the K3s Hetzner cluster | `cat ~/.kube/config-zapfast-prod \| base64 \| pbcopy` |
| `SLACK_WEBHOOK` | `deploy.yml` (optional, commented) | Slack/Discord deploy notifications | Slack: Incoming Webhooks app; Discord: server webhook URL |

## Notes

- `GITHUB_TOKEN` is provided automatically by Actions and is used for:
  - Logging in to GHCR (`ghcr.io`)
  - Creating GitHub Releases
  - Uploading SARIF reports
- Cosign signing uses **keyless OIDC** (no key secret needed); it relies on the `id-token: write` permission set in `release.yml`.
- The `KUBECONFIG_PROD` value must be **base64 encoded** so it survives the secret store untouched. Decode in the workflow with `base64 --decode`.

## Environments

The `deploy.yml` workflow uses GitHub Environments (`staging`, `production`).
For each environment configure under `Settings > Environments`:

- **production**: required reviewers (renatokeys), wait timer (optional), and (recommended) a separate `KUBECONFIG_PROD` secret scoped to the environment.
- **staging**: no required reviewers; staging kubeconfig can be reused or set as `KUBECONFIG_PROD` at the environment level.

## Variables (not secrets)

None required at this point. If the registry or image name changes, edit the `env:` block at the top of `release.yml`.
