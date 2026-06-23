//go:build !windows

package main

import "context"

func isWindowsService() (bool, error)                   { return false, nil }
func runAsWindowsService(_ func(context.Context)) error { return nil }
