//go:build tools

// Package tools pins mobile-build dependencies into go.mod so `go mod
// tidy` keeps them: gomobile bind generates packages that import
// gomobile/bind (and bind/objc on iOS builds), so the module itself must
// resolve them even though no production code imports it directly.
// Excluded from normal builds via the `tools` tag.
package tools

import (
	_ "github.com/sagernet/gomobile/bind"
)
