// Package lazymobile holds the files that are built into the program.
package lazymobile

import _ "embed"

// Changelog is CHANGELOG.md, so the notes of each version can be read without a network.
//
//go:embed CHANGELOG.md
var Changelog string
