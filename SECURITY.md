# Security policy

## Reporting a vulnerability

Please do not open a public issue. Use the repository's private report instead:
[Report a vulnerability](https://github.com/0xrnp/lazymobile/security/advisories/new).

Include what you did, what happened, and the version from `lazymobile version`. This is a
one-person project, so replies are best effort. I will say whether I can reproduce it, and tell you
when a fix is released.

## What counts

lazymobile runs other programs (`xcrun`, `adb`, `emulator`, `eas`) and shows output from devices and
apps, so the cases I care most about are:

- Device or app output that reaches your terminal as control sequences instead of text.
- A device name, app id, address or setting that ends up running something you did not ask for.
- `eas` or a build being started without a confirmation.
- State or saved log files that other users on the machine can read.

## Supported versions

Only the latest release gets fixes.
