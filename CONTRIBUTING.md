# Contributing to lazymobile

Bug reports, fixes and ideas are welcome.

## Before you start

- A bug: open an issue and include the output of `lazymobile doctor`.
- A small fix or a typo: send a pull request.
- Anything bigger: open an issue first, so we can agree on the shape before you write code.

The most useful thing you can test is anything the Status table in the README lists as "fake tools
only". If you have the hardware, say what you ran and what happened.

## Set up

You need Go 1.26.7 or newer (see `go.mod`).

```sh
make check    # formatting, vet, staticcheck, and the tests with the race detector
make vuln     # known vulnerabilities (needs the network)
make build    # ./lazymobile
```

The tests answer for `xcrun`, `adb`, `emulator` and `eas` with fakes, so they run anywhere and never
touch a real device or an Expo account. A fix for a parsing or tool-output problem should come with
a test built from the real output, with names and ids replaced.

## How the code is arranged

- Programs are run through `run.Runner` as argument lists, never as shell strings.
- Backends report failures as `*apperr.Error`, with a hint that says what to do next.
- The dashboard and the plain commands both go through `mobile.Manager`, so they cannot disagree.
- Text that comes from a device or an app is cleaned (`tui.clean`) before it is drawn.

## Making a change

1. Branch from `main`. Name the branch for the kind of change: `fix/...`, `feat/...`, `docs/...` or
   `chore/...`. If you do not have write access, fork the repository first.
2. Keep one concern per pull request.
3. Run `make check`. CI runs the same command on Linux and macOS.
4. Open the pull request against `main`. Its title becomes the commit message and the line in the
   release notes, so write it as a short sentence in the imperative, like the recent history:
   "Show the app picker in the README".

Pull requests are squash merged.

## Releasing

For the maintainer. Versions follow [semantic versioning](https://semver.org). Before 1.0, a minor
version may change behaviour.

1. `main` is green in CI.
2. Tag it and push the tag:

   ```sh
   git switch main && git pull
   git tag -a v0.3.0 -m "lazymobile v0.3.0"
   git push origin v0.3.0
   ```

3. The `release` workflow runs `make check`, builds the macOS and Linux binaries with GoReleaser,
   publishes the GitHub release, and updates the Homebrew cask in `0xrnp/homebrew-tap`. It needs
   the `HOMEBREW_TAP_TOKEN` secret.
4. Read the release notes, then check `brew upgrade lazymobile` and `go install` pick it up.

The notes list the commit subjects since the last tag (`changelog` in `.goreleaser.yaml`), so write
each as a short sentence in the imperative. The maintainer commits to `main` directly, and runs
`make check` first. Pull requests from others are squash merged, and their title is the subject.

Never move or delete a published tag. The Go module proxy keeps the first copy it saw, so a moved
tag leaves `go install` and Homebrew users with different code. If a release is bad, fix it and
tag the next patch version.
