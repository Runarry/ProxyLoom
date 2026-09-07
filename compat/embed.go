// Package compat embeds the M0 core lock and P0 combination list.
package compat

import _ "embed"

//go:embed cores.lock.yaml
var CoresLockYAML []byte

//go:embed p0-combinations.yaml
var CombinationsYAML []byte
