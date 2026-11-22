# Releasing

[Documentation index](wiki/README.md) · [简体中文](RELEASING.zh_CN.md)

This repository publishes versioned binaries to GitHub Releases when a tag is pushed.

## What gets published

For each tag like `v1.2.3`, GitHub Actions builds release archives for:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`
- `windows/arm64`

Each archive includes:

- the `epusdt` and `chainscanner` binaries
- `.env.example`

Windows artifacts are published as `.zip`. Other platforms are published as `.tar.gz`.

Each release also publishes:

- a `SHA256SUMS` checksum file
- a GitHub build provenance attestation for the checksum file

## How to cut a release

1. Make sure the release commit has already landed on the default branch.
2. Create an annotated tag:

```bash
git tag -a v1.0.0 -m "release v1.0.0"
```

3. Push the tag:

```bash
git push origin v1.0.0
```

4. Wait for the `release` workflow to finish on GitHub.
5. Download the generated binaries from the GitHub Release page.

## Verify a release

Verify the GitHub build provenance attestation:

```bash
gh attestation verify SHA256SUMS -R GMwalletApp/epusdt
```

## Local validation

Run a snapshot build locally before pushing a tag:

```bash
cd src
go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean
```

The resulting artifacts will be generated under `src/dist/`.

## Release candidates

If you use tags like `v1.2.3-rc1`, GoReleaser will mark the GitHub release as a prerelease.

For a final tag like `v1.2.3`, the release workflow forces GoReleaser to compare against the previous stable tag instead of the latest `rc` tag. This keeps the final changelog focused on stable-to-stable changes.
