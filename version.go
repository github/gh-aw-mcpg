package main

// Build-time variables set during release builds
var (
	// Version is the semantic version of the binary (e.g., "v1.0.0")
	// Set via -ldflags "-X main.Version=<version>" during build
	Version = "dev"
)
