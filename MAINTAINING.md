# Maintaining a release

Releases are driven entirely by GitHub Actions. Nothing is published by hand.

**1. Configure the token** (once). The cask is pushed to the separate
`john-nash-rs/homebrew-tap` repository, so it needs a token the workflow's own
`GITHUB_TOKEN` cannot provide. The tap already exists and is initialised — see
[homebrew-tap/README.md](homebrew-tap/README.md).

```sh
gh secret set HOMEBREW_TAP_TOKEN --repo john-nash-rs/captaincode
```

Use a fine-grained token with contents read-write on `homebrew-tap` only. It
cannot be named `GITHUB_PERSONAL_AUTH_TOKEN` as some GoReleaser guides
suggest — GitHub reserves the `GITHUB_` prefix and rejects the name.

Verify the token without pasting it into a chat or a file:

```sh
read -s TOKEN && echo
curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $TOKEN" \
  https://api.github.com/repos/john-nash-rs/homebrew-tap
```

`200` works. `401` means GitHub does not recognise the value at all — wrong,
revoked, or expired. `403` means it is valid but lacks write access.

**2. Tag and push.**

```sh
VERSION=vX.Y.Z # replace with the next release tag, for example v0.6.0
git tag -a "$VERSION" -m "CaptainCode $VERSION"
git push origin "$VERSION"
```

The workflow tests Linux and Windows, validates the GoReleaser config, builds six OS and architecture combinations, publishes the archives with `checksums.txt`, and then attempts to update the Homebrew cask.

**Verifying without publishing.** Build the exact artifacts locally:

```sh
goreleaser check
goreleaser release --snapshot --clean --skip=publish
ls dist/
sh -cd dist && shasum -a 256 -c checksums.txt
```

### Things that will bite you

Every item here was hit while publishing 0.1.0. They are properties of the
environment, not of this code.

**Releases cannot be re-run.** GitHub rejects a second upload of an asset that
already exists, and the failure happens before the cask is pushed:

```
422 Validation Failed [{Resource:ReleaseAsset Field:name Code:already_exists}]
```

So re-running the workflow is not a way to repair a broken cask. To re-release
the same version, delete the assets or the release first:

```sh
VERSION=vX.Y.Z # the failed or incomplete release tag
gh release delete "$VERSION" --repo john-nash-rs/captaincode --yes
git push origin ":refs/tags/$VERSION"
```

That leaves a window with no binaries, so prefer a new version number.

**A failed cask push does not roll back the release.** On 0.1.0 the archives
published successfully and only the tap push failed; the release stayed live
while the workflow reported failure. Publish the cask by hand from `dist/` and
nothing is lost.

**The verify gate cannot run on `ubuntu-24.04`.** The sandbox probes build a
network namespace and add a loopback address inside it, and that runner image
runs jobs in a container which forbids `RTM_NEWADDR`, so bubblewrap fails with
`Failed RTM_NEWADDR: Operation not permitted`. The gate runs on `ubuntu-22.04`
for that reason. Do not bump it without moving to a runner that permits these
namespaces.

**Do not validate the config with `go run`.** GoReleaser 2.18.3 requires Go
1.27.1 or newer, and the project pins Go 1.26.9 with `GOTOOLCHAIN=local`. The
workflow installs GoReleaser as a binary in `install-only` mode instead, so one
version both validates the configuration and performs the release.
