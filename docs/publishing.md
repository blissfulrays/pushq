# Publishing the SDKs

Releases are automatic. When a change lands on `main` and CI passes there,
`.github/workflows/release.yml` tags the next version and publishes it
everywhere: binaries, the Docker image, and all five SDKs.

## How versions are picked

| Latest release | Next automatic release |
|---|---|
| `v0.1.0-beta.4` (prerelease) | `v0.1.0-beta.5` |
| `v0.1.0` (stable) | `v0.1.1` |

- **Nothing ships, nothing publishes.** A release only happens when something
  under `cmd/`, `internal/`, `web/`, `sdks/`, `api/`, `go.mod`, `go.sum`,
  `Dockerfile` or `.goreleaser.yaml` changed since the last release. Docs and
  CI-only merges are skipped.
- **Skip one on purpose:** put `[skip release]` in the merge commit message.
- **Cut a specific version** (a stable `0.1.0`, a minor `0.2.0`, a new
  `0.2.0-rc.1`, …): Actions → *release* → *Run workflow* with that version.
  The workflow creates the tag. Later automatic releases continue from it.
- **Or push a tag yourself:** `git tag v0.2.0 && git push origin v0.2.0`.

Releases run one at a time, so two quick merges get consecutive versions. A
failed CI run on `main` never releases.

## Where each part goes

| Target | Registry | Credentials needed | Job no-ops without them |
|---|---|---|---|
| Binaries | GitHub Releases | none (built-in token) | — |
| Docker | ghcr.io | none (built-in token) | — |
| TypeScript | npm | Trusted Publishing (or `NPM_TOKEN` fallback) | no — fails loudly |
| Python | PyPI | Trusted Publishing (no secret) | no — fails loudly |
| Java | Maven Central | 4 secrets (see below) | yes |
| C# / .NET | NuGet | Trusted Publishing + `NUGET_USER` secret | yes |
| Go | none — just a git tag | none | — |

Maven and NuGet skip themselves until their secrets exist. npm and PyPI always try to
publish and fail loudly when their trusted publisher isn't set up: a silent
skip would look exactly like a release that quietly shipped nothing.

---

## Before the first release

**Re-check the names.** `pushq` was free on npm, PyPI, and Docker Hub when this
project started, but names get taken. Whoever publishes first claims it:

```bash
npm view pushq            # "404 Not Found" = still available
pip index versions pushq  # or: curl -s -o /dev/null -w '%{http_code}\n' https://pypi.org/pypi/pushq/json
```

**Decide the version.** Everything shares the tag's version — tag `v0.1.0` and
npm, PyPI, and Maven all publish `0.1.0`. The workflow rewrites the version
strings at build time, so the committed `0.1.0` / `0.1.0-SNAPSHOT` values are
just placeholders you never have to keep in sync by hand.

Start at `0.1.0` and stay `0.x` while the API can still change — that
communicates "not stable yet" in every ecosystem's conventions.

---

## One-time setup per registry

### npm (TypeScript SDK)

npm uses **Trusted Publishing**, like PyPI: GitHub proves the workflow's
identity with a short-lived OIDC token, so there's no npm token to store or
rotate. A token that expires (granular tokens last at most 90 days) silently
breaks every automatic release; that's how `0.1.0-beta.5` missed npm.

1. Sign in at npmjs.com as an owner of `pushq`.
2. Package **pushq → Settings → Trusted Publisher → GitHub Actions**:
   - Organization or user: `blissfulrays`
   - Repository: `pushq`
   - Workflow filename: `release.yml`
   - Environment: leave empty
3. Re-run the release (see below). Once a publish succeeds, delete the
   `NPM_TOKEN` repository secret. Optionally set the package's *Publishing
   access* to "Require two-factor authentication and disallow tokens", which
   leaves Trusted Publishing as the only way in.

Packages published this way get provenance automatically: npm shows a "Built
and signed on GitHub Actions" badge linking to the exact workflow run.

**Fallback:** until Trusted Publishing is set up, the job uses the `NPM_TOKEN`
secret (a granular access token with read and write on `pushq`). npm reports an
expired or wrong token as `E404 Not Found - PUT https://registry.npmjs.org/pushq`.

### Re-running a release

If one registry failed (say npm), fix its credentials and run the *release*
workflow by hand with the **same version**, e.g. `0.1.0-beta.5`. It rebuilds
from that version's tagged commit, and anything already published (the GitHub
release and binaries, the npm version, PyPI files, the Go tag) is skipped.
Docker re-pushes the same image tag. For a single failed job you can also use
*Re-run failed jobs* on the original run.

### PyPI (Python SDK)

PyPI uses **Trusted Publishing** — no API token to store or rotate. GitHub
proves the workflow's identity with a short-lived OIDC token.

1. Create an account at pypi.org, enable 2FA.
2. Account settings → **Publishing** → *Add a new pending publisher*:
   - PyPI Project Name: `pushq`
   - Owner: `blissfulrays`
   - Repository name: `pushq`
   - Workflow name: `release.yml`
   - Environment name: `pypi`
3. GitHub repo → Settings → **Environments** → New environment named `pypi`.
   The name must match the publisher exactly — it is part of the identity PyPI
   verifies. (Optional: add yourself as a required reviewer so a release pauses
   for approval before it reaches PyPI.)

There is no token and no opt-in flag; once the publisher and environment exist,
the release publishes.

"Pending publisher" is the right choice pre-first-release — it authorizes the
project name before the project exists.

### Maven Central (Java SDK)

The fiddliest one; budget an hour for the first time. Two independent things
are required: **namespace ownership** and **GPG signatures**.

**1. Claim the namespace.** Sign in at central.sonatype.com with GitHub. Add
namespace `io.github.blissfulrays`. Because it's an `io.github.*` namespace
matching your GitHub account, verification is automatic once you complete the
prompted GitHub check (typically creating a temporary public repo named with
the token Sonatype shows you). Verified namespaces persist — you only do this
once for all future artifacts.

**2. Generate a publishing token.** Central Portal → your account → **Generate
User Token**. It gives you a username/password pair (not your login).

**3. Create a GPG key** — Central requires every artifact to be signed:

```bash
gpg --full-generate-key          # RSA, 4096 bits, no expiry (or a long one)
gpg --list-secret-keys --keyid-format=long   # note the long key id

# Publish the public half — Central verifies signatures against a keyserver
gpg --keyserver keyserver.ubuntu.com --send-keys <KEY_ID>

# Export the private half for CI (keep this output secret)
gpg --armor --export-secret-keys <KEY_ID>
```

**4. Add four repository secrets:**

| Secret | Value |
|---|---|
| `MAVEN_CENTRAL_USERNAME` | portal token username |
| `MAVEN_CENTRAL_PASSWORD` | portal token password |
| `GPG_PRIVATE_KEY` | the full `-----BEGIN PGP PRIVATE KEY BLOCK-----` block |
| `GPG_PASSPHRASE` | the key's passphrase |

The POM keeps all of this in a `release` profile, so day-to-day `mvn package`
never needs a GPG key.

### NuGet (C# / .NET SDK)

NuGet also uses **Trusted Publishing**: the official `NuGet/login` action
exchanges GitHub's OIDC token for an API key that lives for an hour, so no
long-lived key is stored anywhere.

1. Sign in at nuget.org (create an account if needed; enable 2FA).
2. Account menu → **Trusted Publishing** → *Create*:
   - Repository Owner: `blissfulrays`
   - Repository: `pushq`
   - Workflow File: `release.yml`
   - Environment: leave empty
   - Package owner: your account
3. GitHub repo → Settings → Secrets and variables → Actions → new secret
   `NUGET_USER` = your **nuget.org profile name** (not your email). It is how
   `NuGet/login` knows which account's policy to use.

The package ID `Pushq` was unclaimed when the SDK was added; the first
publish claims it for the account above. Packages are signed by nuget.org and
ship a `.snupkg` with symbols and SourceLink, so debuggers step into the SDK
source.

Until `NUGET_USER` exists the job skips itself with a notice.

### Go SDK

Nothing to publish and no account to create. Go modules are fetched straight
from git tags, and the proxy indexes them on first request.

The only requirement is the tag name: a module in `sdks/go` is versioned by the
tag **`sdks/go/v0.1.0`**, not the repo-root `v0.1.0`. The `go-sdk-tag` job
creates it for you on every release.

---

## Beta / prerelease

Any tag with a hyphen is treated as a prerelease, and every registry is told
to keep it off the default install path:

```bash
git tag v0.1.0-beta.1
git push origin v0.1.0-beta.1
```

| Registry | Published as | How users get it | Default install affected? |
|---|---|---|---|
| npm | `0.1.0-beta.1` under dist-tag `beta` | `npm install pushq@beta` | no — `latest` unmoved |
| PyPI | `0.1.0b1` (PEP 440 normalizes it) | `pip install --pre pushq` | no — pip skips prereleases |
| Maven Central | `0.1.0-beta.1` | pin the version | no |
| NuGet | `0.1.0-beta.1` (SemVer 2 prerelease) | `dotnet add package Pushq --prerelease` | no — prereleases need opting in |
| Go | tag `sdks/go/v0.1.0-beta.1` | `go get …/sdks/go@v0.1.0-beta.1` | no — `@latest` skips prereleases |
| Docker | `:0.1.0-beta.1` only | pin the tag | no — `:latest` unmoved |
| GitHub | release marked "Pre-release" | — | not shown as Latest |

The dist-tag is derived from the version: `-beta.N` → `beta`, `-rc.N` → `rc`,
`-alpha.N` → `alpha`. Iterate with `beta.2`, `beta.3`, … — every version is
immutable, so a fix always means a new number.

Promote to stable by tagging `v0.1.0` when you're happy.

### Two first-time surprises

**npm points `latest` at your first publish, even with `--tag beta`.** Every
package must have a `latest`, so npm sets it when the package has no other
versions. It self-corrects the moment you publish a stable version without
`--tag`. Until then `npm install pushq` resolves to the beta — harmless when
no stable exists, since there's nothing else to install. To repoint it by hand
later: `npm dist-tag add pushq@0.1.0 latest`.

**GHCR packages are private by default.** The first push creates the package
private, so `docker pull ghcr.io/blissfulrays/pushq` fails anonymously until
you open the package's settings on GitHub and change its visibility to public
(and, while there, link it to the repository).

## Releasing

```bash
# 1. Everything green locally
make test
make test-pg          # if you have a Postgres to point at

# 2. Tag and push
git tag v0.1.0
git push origin v0.1.0
```

Then watch the run in the repo's Actions tab. On success you'll have:

- GitHub Release with binaries for macOS/Linux/Windows × amd64/arm64
- `ghcr.io/blissfulrays/pushq:0.1.0` and `:latest`
- `pushq@0.1.0` on npm, `pushq==0.1.0` on PyPI, `io.github.blissfulrays:pushq:0.1.0` on Central
- `sdks/go/v0.1.0` tag for `go get`

You can also trigger it from the Actions tab via **Run workflow** and type a
version — useful for re-running a single failed registry without cutting a new
tag.

### Verify

```bash
npm view pushq version
pip download pushq==0.1.0 -d /tmp/x --no-deps
go list -m github.com/blissfulrays/pushq/sdks/go@v0.1.0
curl -s https://api.nuget.org/v3-flatcontainer/pushq/index.json   # NuGet: lists published versions
# Maven Central: search central.sonatype.com; expect 10–30 min before it's
# searchable, though direct coordinate resolution works sooner.
```

---

## Publishing by hand

Useful for the very first release, when you want to watch each step.

```bash
V=0.1.0

# npm
cd sdks/typescript
npm pkg set version=$V
npm run build
npm pack --dry-run          # inspect what ships (should be dist/ + README only)
npm publish --access public

# PyPI
cd sdks/python
sed -i '' "s/^version = .*/version = \"$V\"/" pyproject.toml
sed -i '' "s/^__version__ = .*/__version__ = \"$V\"/" src/pushq/__init__.py
python -m build
python -m twine upload dist/*

# Maven Central (needs the GPG key + ~/.m2/settings.xml, below)
cd sdks/java
mvn versions:set -DnewVersion=$V -DgenerateBackupPoms=false
mvn -P release deploy

# Go
git tag sdks/go/v$V && git push origin sdks/go/v$V
```

`~/.m2/settings.xml` for a manual Maven release:

```xml
<settings>
  <servers>
    <server>
      <id>central</id>
      <username>PORTAL_TOKEN_USERNAME</username>
      <password>PORTAL_TOKEN_PASSWORD</password>
    </server>
  </servers>
</settings>
```

To stage without going live, set `<autoPublish>false</autoPublish>` in the
POM's release profile — the upload lands in the Central Portal where you can
inspect it and click Publish (or drop it).

---

## Gotchas

**Versions are immutable everywhere.** npm, PyPI, and Maven Central all refuse
a re-upload of an existing version, and Central can't be un-published at all.
If a release is broken, ship `0.1.1`. Practice on a throwaway version like
`0.0.1` if you want to see the pipeline run end to end.

**npm 404 on publish** — usually the name was taken, or the token lacks write
scope on a package that doesn't exist yet (use "All packages" for the first
publish).

**PyPI `invalid-publisher`** — the pending publisher's owner/repo/workflow/
environment must match exactly, including the `pypi` environment existing in
GitHub repo settings.

**Maven `401`** — you used your Sonatype login instead of the generated portal
token. **`gpg: signing failed`** in CI usually means `GPG_PASSPHRASE` is wrong
or the exported key was the public half. Central rejecting a signature usually
means the public key never reached a keyserver.

**`go get` resolves an old version** — the module proxy caches aggressively;
`GOPROXY=direct go get ...` or wait a few minutes.

**Python's version lives in two files** (`pyproject.toml` and
`src/pushq/__init__.py`). CI rewrites both; keep them together if you edit by
hand.

**Don't delete `src/pushq/py.typed`.** It's an empty file, but without it
(PEP 561) every type checker silently ignores the SDK's annotations and treats
`import pushq` as untyped — no error, just no type safety for users. CI asserts
it ships inside the wheel.

**Java's `maven.compiler.release=11` is load-bearing.** It pins both the API
surface and the bytecode level regardless of which JDK builds the artifact —
CI deliberately builds on JDK 21 and asserts class-file major version 55, so a
toolchain upgrade can't silently strip Java 11 support out from under users.
CI also asserts `Automatic-Module-Name` and runs the `release` profile on every
PR, so Javadoc errors surface long before release day.
