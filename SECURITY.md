Thanks for helping make GitHub safe for everyone.

# Security

GitHub takes the security of our software products and services seriously, including all of the open source code repositories managed through our GitHub organizations, such as [GitHub](https://github.com/GitHub).

Even though [open source repositories are outside of the scope of our bug bounty program](https://bounty.github.com/index.html#scope) and therefore not eligible for bounty rewards, we will ensure that your finding gets passed along to the appropriate maintainers for remediation.

## Reporting Security Issues

If you believe you have found a security vulnerability in any GitHub-owned repository, please report it to us through coordinated disclosure.

**Please do not report security vulnerabilities through public GitHub issues, discussions, or pull requests.**

Instead, please send an email to opensource-security[@]github.com.

Please include as much of the information listed below as you can to help us better understand and resolve the issue:

- The type of issue (e.g., buffer overflow, SQL injection, or cross-site scripting)
- Full paths of source file(s) related to the manifestation of the issue
- The location of the affected source code (tag/branch/commit or direct URL)
- Any special configuration required to reproduce the issue
- Step-by-step instructions to reproduce the issue
- Proof-of-concept or exploit code (if possible)
- Impact of the issue, including how an attacker might exploit the issue

This information will help us triage your report more quickly.

## Policy

See [GitHub's Safe Harbor Policy](https://docs.github.com/en/github/site-policy/github-bug-bounty-program-legal-safe-harbor#1-safe-harbor-terms)

## SSHHole (CVE-2026-123158) applicability

The published MCP Gateway **v0.5.8** runtime images inspected on **2026-11-12**
do not contain OpenSSH packages or an `sshd` executable. They are **not affected**
by SSHHole; no OpenSSH package upgrade is needed in these images.

[OpenSSH's 10.6p2 release notes](https://www.openssh.org/releasenotes.html#10.6p2)
describe a password authentication bypass affecting musl-based Linux systems
running an SSH server without active PAM authentication.
[Alpine's advisory](https://alpinelinux.org/posts/2026-11-05-openssh-password-authentication-bypass/)
explicitly excludes containers that do not run `sshd`. Its fixed OpenSSH
package for Alpine 3.24 is `10.3_p1-r2`; distro backports must be checked using
the complete package revision, not the client output of `ssh -V`.

### Published-image evidence

The assessment inspected every layer of both Linux architectures in
`ghcr.io/github/gh-aw-mcpg:v0.5.8`, including the final populated
`/lib/apk/db/installed` database and filesystem entries. The image index digest
was `sha256:e237aabf51d4485474b9355649b811ca6c1db22359eeb1860832fc1fb5a7a347`.

| Platform | Image manifest digest | OpenSSH packages / `sshd` files |
| --- | --- | --- |
| linux/amd64 | `sha256:dab817040f9666831719bfc4517bff30f4eaae625b4ee7e8d492600b7e0b317e` | None |
| linux/arm64 | `sha256:f8a03b4bd8ce3b6e0c5fb52386599a868bf4d1af4fa577bef8ff49d451299b74` | None |

These images report Alpine 3.24.2. That base-release number alone does not
establish exposure: the affected SSH server is absent. The runtime Dockerfile
installs container runtimes and their dependencies, not an SSH server, and the
entrypoint starts the gateway rather than `sshd`. The development container uses
Debian Bookworm (glibc), not the musl authentication stack affected by this bypass;
it is not the shipped gateway runtime image.

### Keeping the runtime free of SSH servers

The final Dockerfile build step rejects OpenSSH server packages (including PAM
and server subpackages) or an `sshd` executable on the runtime search path,
after all packages and build artifacts have been installed. Package-query
failures also fail the build. `TestDockerfileRejectsSSHServer` exercises this
check with absent, client-only, server, PAM-server, unpackaged-daemon, and
package-query-failure cases.

This is a preventive build invariant, **not a patch to an installed OpenSSH
server**. It does not update existing deployments or cover user-supplied backend
containers, derived images that install SSH, or host SSH services. Assess those
separately; affected servers need a fixed package and an `sshd` restart as
described in the Alpine advisory. Future gateway images must be reassessed if
their contents change.

The published-image assessment used registry-layer inspection because a local
Docker daemon was unavailable. It did not execute the images or reproduce an
authentication attempt; absence of the server, not a login test, establishes
non-applicability for the inspected digests.

## Software Bill of Materials (SBOM)

We generate Software Bill of Materials (SBOM) for this project to provide complete visibility into the dependency tree, enabling compliance reporting, vulnerability tracking, and supply chain risk assessment.

### SBOM Generation

SBOMs are automatically generated on every release and attached to GitHub releases as downloadable assets.

Both SPDX and CycloneDX formats are generated to ensure compatibility with different compliance and security tools.

### Local SBOM Generation

To generate an SBOM locally, first install [syft](https://github.com/anchore/syft):

```bash
# Install syft
curl -sSfL https://raw.githubusercontent.com/anchore/syft/main/install.sh | sh -s -- -b /usr/local/bin

# Generate SBOMs
syft dir:. -o spdx-json > sbom.spdx.json
syft dir:. -o cyclonedx-json > sbom.cdx.json
```

This produces two files:
- `sbom.spdx.json` - SBOM in SPDX JSON format
- `sbom.cdx.json` - SBOM in CycloneDX JSON format

### SBOM Contents

The generated SBOMs include:
- All direct and transitive Go dependencies
- Package versions and licenses
- Package hashes for integrity verification
- Dependency relationships
