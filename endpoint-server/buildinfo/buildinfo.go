package buildinfo

// BuildTime is stamped at compile time via -ldflags.
// Defaults to "dev" for local builds that bypass the Makefile.
var BuildTime = "dev"
