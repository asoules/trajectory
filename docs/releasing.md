# Releasing Trajectory

Git tags are the release boundary. Pushing a semantic-version tag starts the
`Release` GitHub Actions workflow, which tests and builds Trajectory natively for
macOS and Linux on Intel and ARM. The workflow publishes four archives and a
`checksums.txt` file to a GitHub release.

The version, commit, and commit date are embedded in every executable. Release
jobs pin Go 1.27.1, use `-trimpath`, disable automatic VCS stamping, and rebuild
the executable to verify identical bytes. A small Go archiver fixes member order,
owners, modes, timestamps, and gzip metadata from the source commit time, then the
workflow creates each archive twice and compares it byte-for-byte. Native runner
images can still update their C compiler, so reproducibility is enforced within a
release run rather than promised across future runner-image revisions.

## Publish a release

1. Make sure CI is green on `main` and the worktree is clean.
2. Choose a semantic version such as `v0.1.0`.
3. Create and push an annotated tag:

   ```sh
   git tag -a v0.1.0 -m "Trajectory v0.1.0"
   git push origin v0.1.0
   ```

4. Wait for all four native build jobs and the publish job to succeed.
5. Download an archive and `checksums.txt`, verify the checksum independently,
   and smoke-test `trajectory version` and `trajectory serve -demo`.
6. Run `install.sh` against the published release on macOS and Linux.

Do not move or recreate a release tag. Publish a new patch version for corrections.

## Homebrew

The first-party [`asoules/homebrew-tap`](https://github.com/asoules/homebrew-tap)
publishes a `trajectory` formula referencing the four immutable release archives
and their SHA-256 values. Users install it with:

```sh
brew install asoules/tap/trajectory
```

For each release, update all four formula URLs and SHA-256 values, then run
`brew style`, `brew audit --strict --online --os=all --arch=all`, and
`brew test`. The formula test must exercise real behavior beyond `version` or
`help`: it starts a demo server on a temporary port, requests `/healthz`, and
stops the process. Consider Homebrew Core only after Trajectory has multiple
stable releases and meaningful third-party usage.
