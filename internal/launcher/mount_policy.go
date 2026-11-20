package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mountspec"
)

var logMountPolicy = logger.ForFile()

// AllowedMountRootsEnvVar names the environment variable used by operators to
// override the trusted host mount roots. The value is a comma-separated list of
// "path" or "path:ro" / "path:rw" entries (defaults to read-only).
const AllowedMountRootsEnvVar = "MCP_GATEWAY_ALLOWED_MOUNT_ROOTS"

// mountBypassOptions are container runtime options that could expose host paths
// without going through a structured mount declaration. They are rejected so a
// backend configuration cannot escape the mount policy.
var mountBypassOptions = map[string]string{
	"--mount":        "use the structured 'mounts' field ('source:dest:mode') instead of --mount",
	"--volumes-from": "volumes cannot be inherited from other containers",
	"--privileged":   "privileged containers can access arbitrary host devices",
	"--device":       "host devices cannot be exposed to MCP servers",
	"--rootfs":       "host directories cannot be used as container root filesystems",
}

// MountRoot is a trusted host directory that container-backed MCP servers are
// permitted to mount from. Writable is an explicit, trusted policy decision:
// mounts under a root are forced read-only unless the root allows writes.
type MountRoot struct {
	// Path is the host directory root (canonicalized when the policy is built).
	Path string
	// Writable reports whether "rw" mounts are permitted under this root.
	Writable bool
}

// MountPolicy is the typed configuration boundary that determines which host
// paths a container-backed MCP server may bind-mount. It is owned by the
// launcher (the trusted component that starts backend processes) and is never
// derived from MCP server configuration.
type MountPolicy struct {
	// Roots is the allowlist of host roots. An empty list denies all mounts.
	Roots []MountRoot
}

// DefaultMountPolicy builds the default allowlist: the agent workspace
// (read-only) and the temporary directory root (read-write, used for gateway
// logs and large payload exchange). Operators may override the allowlist with
// AllowedMountRootsEnvVar.
func DefaultMountPolicy() MountPolicy {
	if raw := strings.TrimSpace(envutil.GetEnvString(AllowedMountRootsEnvVar, "")); raw != "" {
		policy := parseMountRoots(raw)
		logMountPolicy.Printf("Mount policy overridden by %s: %d root(s)", AllowedMountRootsEnvVar, len(policy.Roots))
		return policy
	}

	var roots []MountRoot
	if workspace := strings.TrimSpace(envutil.GetEnvString("GITHUB_WORKSPACE", "")); workspace != "" {
		roots = append(roots, MountRoot{Path: workspace, Writable: false})
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		roots = append(roots, MountRoot{Path: cwd, Writable: false})
	}
	roots = append(roots, MountRoot{Path: os.TempDir(), Writable: true})

	policy := MountPolicy{Roots: canonicalizeRoots(roots)}
	logMountPolicy.Printf("Default mount policy: %d allowed root(s)", len(policy.Roots))
	return policy
}

// parseMountRoots parses a comma-separated allowlist of "path[:ro|:rw]" entries.
// Malformed or non-absolute entries are ignored (default-deny).
func parseMountRoots(raw string) MountPolicy {
	var roots []MountRoot
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		writable := false
		switch {
		case strings.HasSuffix(entry, ":rw"):
			writable = true
			entry = strings.TrimSuffix(entry, ":rw")
		case strings.HasSuffix(entry, ":ro"):
			entry = strings.TrimSuffix(entry, ":ro")
		}
		if !filepath.IsAbs(entry) {
			logMountPolicy.Printf("Ignoring non-absolute mount root entry in %s", AllowedMountRootsEnvVar)
			continue
		}
		roots = append(roots, MountRoot{Path: entry, Writable: writable})
	}
	return MountPolicy{Roots: canonicalizeRoots(roots)}
}

// canonicalizeRoots resolves symlinks and cleans each root path, dropping roots
// that cannot be canonicalized. Longer (more specific) roots are ordered first
// so a writable sub-root takes precedence over a read-only parent.
func canonicalizeRoots(roots []MountRoot) []MountRoot {
	canonical := make([]MountRoot, 0, len(roots))
	seen := make(map[string]bool, len(roots))
	for _, root := range roots {
		path, err := canonicalizePath(root.Path)
		if err != nil {
			logMountPolicy.Printf("Ignoring mount root that cannot be canonicalized: %v", err)
			continue
		}
		if path == string(filepath.Separator) {
			// The filesystem root would allow every host path; default-deny instead.
			logMountPolicy.Printf("Ignoring filesystem-root mount root")
			continue
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		canonical = append(canonical, MountRoot{Path: path, Writable: root.Writable})
	}
	sort.SliceStable(canonical, func(i, j int) bool {
		return len(canonical[i].Path) > len(canonical[j].Path)
	})
	return canonical
}

// canonicalizePath returns an absolute, symlink-resolved, cleaned version of
// path. Because a host directory may legitimately not exist yet (the container
// runtime creates it), symlinks are resolved on the longest existing ancestor
// and the remaining components are appended.
func canonicalizePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path is not absolute")
	}
	cleaned := filepath.Clean(path)

	remainder := ""
	current := cleaned
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			if remainder == "" {
				return filepath.Clean(resolved), nil
			}
			return filepath.Clean(filepath.Join(resolved, remainder)), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("cannot resolve path")
		}
		parent := filepath.Dir(current)
		if parent == current {
			// Reached the filesystem root without finding an existing ancestor.
			return cleaned, nil
		}
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
	}
}

// isUnderRoot reports whether canonical path is the root itself or nested under it.
func isUnderRoot(path, root string) bool {
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// mountDeclaration is a parsed "source:dest:mode" bind-mount declaration.
type mountDeclaration struct {
	source   string
	dest     string
	writable bool
}

// parseMountDeclaration parses a structured bind-mount specification.
func parseMountDeclaration(spec string) (mountDeclaration, error) {
	mount, err := mountspec.Parse(spec)
	if err != nil {
		return mountDeclaration{}, err
	}
	return mountDeclaration{source: mount.Source, dest: mount.Destination, writable: mount.Writable}, nil
}

// ValidateMount checks a single "source:dest:mode" declaration against the policy.
func (p MountPolicy) ValidateMount(spec string) error {
	decl, err := parseMountDeclaration(spec)
	if err != nil {
		return fmt.Errorf("invalid mount declaration %q: %w", spec, err)
	}

	source, err := canonicalizePath(decl.source)
	if err != nil {
		logMountPolicy.Printf("Mount %q rejected: cannot canonicalize host source", decl.source)
		return fmt.Errorf("mount %q rejected: host source cannot be canonicalized", decl.source)
	}

	// Roots are ordered most-specific first, so the closest enclosing root wins.
	// A read-only root nested inside a writable root therefore narrows access
	// rather than inheriting write permission from its parent.
	for _, root := range p.Roots {
		if !isUnderRoot(source, root.Path) {
			continue
		}
		if decl.writable && !root.Writable {
			logMountPolicy.Printf("Mount %q rejected: read-write access denied under root %q", decl.source, root.Path)
			return fmt.Errorf("mount %q rejected: read-write access to this host root is not permitted by mount policy", decl.source)
		}
		logMountPolicy.Printf("Mount %q allowed under root %q (writable=%t)", decl.source, root.Path, decl.writable)
		return nil
	}

	logMountPolicy.Printf("Mount %q rejected: outside all allowed mount roots", decl.source)
	return fmt.Errorf("mount %q rejected: host source is outside the allowed mount roots", decl.source)
}

// ValidateContainerArgs validates the container runtime arguments used to launch
// a backend MCP server. Every bind-mount declaration must satisfy the policy and
// no policy-bypassing runtime option may be present.
func (p MountPolicy) ValidateContainerArgs(args []string) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]

		option := arg
		if idx := strings.Index(arg, "="); idx > 0 {
			option = arg[:idx]
		}
		if reason, denied := mountBypassOptions[option]; denied {
			logMountPolicy.Printf("Container option %q rejected by mount policy", option)
			return fmt.Errorf("container option %q rejected by mount policy: %s", option, reason)
		}

		var spec string
		switch {
		case arg == "-v" || arg == "--volume":
			if i+1 >= len(args) {
				return fmt.Errorf("container option %q rejected by mount policy: missing mount declaration", arg)
			}
			i++
			spec = args[i]
		case strings.HasPrefix(arg, "--volume="):
			spec = strings.TrimPrefix(arg, "--volume=")
		default:
			continue
		}

		if err := p.ValidateMount(spec); err != nil {
			return err
		}
	}
	return nil
}
