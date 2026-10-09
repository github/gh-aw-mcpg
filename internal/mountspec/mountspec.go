// Package mountspec parses container bind-mount declarations.
package mountspec

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var log = logger.ForFile()

// Spec is a parsed bind-mount declaration.
type Spec struct {
	Source      string
	Destination string
	Writable    bool
}

// ErrorKind identifies the invalid part of a mount declaration.
type ErrorKind int

const (
	InvalidFormat ErrorKind = iota
	EmptySource
	EmptyDestination
	RelativeSource
	RelativeDestination
	InvalidOptions
)

// ParseError describes an invalid mount declaration.
type ParseError struct {
	Kind ErrorKind
}

func (e *ParseError) Error() string {
	switch e.Kind {
	case InvalidFormat:
		return "expected 'source:dest:mode'"
	case EmptySource, EmptyDestination:
		return "source and destination must not be empty"
	case RelativeSource:
		return "host source must be an absolute path"
	case RelativeDestination:
		return "container destination must be an absolute path"
	default:
		return "invalid mount options"
	}
}

// Parse parses a "source:dest[:mode]" bind-mount declaration. Omitted mode
// defaults to read-write; mode may contain one "ro" or "rw" option.
func Parse(spec string) (Spec, error) {
	parts := strings.Split(spec, ":")
	log.Printf("Parsing mount spec: parts=%d", len(parts))
	if len(parts) < 2 || len(parts) > 3 {
		log.Printf("Rejecting mount spec: invalid format (expected 2-3 colon-separated parts, got %d)", len(parts))
		return Spec{}, &ParseError{Kind: InvalidFormat}
	}
	if parts[0] == "" {
		log.Print("Rejecting mount spec: source is empty")
		return Spec{}, &ParseError{Kind: EmptySource}
	}
	if parts[1] == "" {
		log.Print("Rejecting mount spec: destination is empty")
		return Spec{}, &ParseError{Kind: EmptyDestination}
	}
	if !filepath.IsAbs(parts[0]) {
		log.Print("Rejecting mount spec: source path is not absolute")
		return Spec{}, &ParseError{Kind: RelativeSource}
	}
	if !filepath.IsAbs(parts[1]) {
		log.Print("Rejecting mount spec: destination path is not absolute")
		return Spec{}, &ParseError{Kind: RelativeDestination}
	}

	mount := Spec{Source: parts[0], Destination: parts[1], Writable: true}
	if len(parts) == 2 {
		log.Print("Mount spec has no explicit mode, defaulting to read-write")
		return mount, nil
	}

	modeSet := false
	for _, option := range strings.Split(parts[2], ",") {
		switch option = strings.TrimSpace(option); option {
		case "ro", "rw":
			if modeSet {
				log.Print("Rejecting mount spec: conflicting mode options")
				return Spec{}, fmt.Errorf("%w: conflicting mount options", &ParseError{Kind: InvalidOptions})
			}
			modeSet = true
			mount.Writable = option == "rw"
		case "":
			log.Print("Rejecting mount spec: empty mode option")
			return Spec{}, fmt.Errorf("%w: empty mount option", &ParseError{Kind: InvalidOptions})
		default:
			log.Print("Rejecting mount spec: unsupported mode option")
			return Spec{}, fmt.Errorf("%w: unsupported mount option", &ParseError{Kind: InvalidOptions})
		}
	}
	log.Printf("Parsed mount spec: writable=%v", mount.Writable)
	return mount, nil
}

// ParseRequiredMode parses a bind-mount declaration that must explicitly
// include its mode.
func ParseRequiredMode(spec string) (Spec, error) {
	parts := strings.Split(spec, ":")
	if len(parts) != 3 {
		log.Printf("Rejecting mount spec: expected exactly 3 colon-separated parts, got %d", len(parts))
		return Spec{}, &ParseError{Kind: InvalidFormat}
	}
	if parts[2] != "ro" && parts[2] != "rw" {
		log.Print("Rejecting mount spec: explicit mode must be ro or rw")
		return Spec{}, &ParseError{Kind: InvalidOptions}
	}
	return Parse(spec)
}
