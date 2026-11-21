package launcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/config"
)

// newTestPolicy builds a policy with a read-only workspace root and a
// read-write temp root, both canonicalized like production policies.
func newTestPolicy(t *testing.T) (policy MountPolicy, workspace string, tmp string) {
	t.Helper()
	base := t.TempDir()
	workspace = filepath.Join(base, "workspace")
	tmp = filepath.Join(base, "tmp")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	require.NoError(t, os.MkdirAll(tmp, 0o755))

	policy = MountPolicy{Roots: canonicalizeRoots([]MountRoot{
		{Path: workspace, Writable: false},
		{Path: tmp, Writable: true},
	})}
	return policy, workspace, tmp
}

func TestMountPolicyValidateMount(t *testing.T) {
	policy, workspace, tmp := newTestPolicy(t)

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "read-only workspace mount allowed",
			spec: workspace + ":/workspace:ro",
		},
		{
			name: "read-only workspace subdirectory allowed",
			spec: filepath.Join(workspace, "repo") + ":/workspace:ro",
		},
		{
			name: "read-write temp mount allowed",
			spec: tmp + ":/payloads:rw",
		},
		{
			name:    "read-write workspace mount rejected",
			spec:    workspace + ":/workspace:rw",
			wantErr: "read-write access",
		},
		{
			name:    "mount without mode defaults to read-write and is rejected",
			spec:    workspace + ":/workspace",
			wantErr: "read-write access",
		},
		{
			name:    "host path outside allowed roots rejected",
			spec:    "/etc:/host-etc:ro",
			wantErr: "outside the allowed mount roots",
		},
		{
			name:    "path traversal escape rejected",
			spec:    filepath.Join(workspace, "..", "..", "etc") + ":/host-etc:ro",
			wantErr: "outside the allowed mount roots",
		},
		{
			name:    "relative source rejected",
			spec:    "workspace:/workspace:ro",
			wantErr: "absolute path",
		},
		{
			name:    "relative destination rejected",
			spec:    workspace + ":workspace:ro",
			wantErr: "absolute path",
		},
		{
			name:    "malformed declaration rejected",
			spec:    workspace,
			wantErr: "invalid mount declaration",
		},
		{
			name:    "too many segments rejected",
			spec:    workspace + ":/workspace:ro:extra",
			wantErr: "invalid mount declaration",
		},
		{
			name:    "unsupported mount option rejected",
			spec:    workspace + ":/workspace:rslave",
			wantErr: "unsupported mount option",
		},
		{
			name:    "empty source rejected",
			spec:    ":/workspace:ro",
			wantErr: "must not be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := policy.ValidateMount(tt.spec)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestMountPolicyRejectsSymlinkEscape(t *testing.T) {
	policy, workspace, _ := newTestPolicy(t)

	outside := t.TempDir()
	link := filepath.Join(workspace, "escape")
	require.NoError(t, os.Symlink(outside, link))

	err := policy.ValidateMount(link + ":/workspace:ro")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside the allowed mount roots")

	// A symlink that stays inside the allowed root remains valid.
	inside := filepath.Join(workspace, "inner")
	require.NoError(t, os.MkdirAll(inside, 0o755))
	innerLink := filepath.Join(workspace, "inner-link")
	require.NoError(t, os.Symlink(inside, innerLink))
	assert.NoError(t, policy.ValidateMount(innerLink+":/workspace:ro"))
}

func TestMountPolicyEmptyPolicyDeniesAllMounts(t *testing.T) {
	var policy MountPolicy
	err := policy.ValidateMount("/tmp:/data:ro")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside the allowed mount roots")
}

func TestMountPolicyValidateContainerArgs(t *testing.T) {
	policy, workspace, tmp := newTestPolicy(t)

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name: "allowed mounts pass",
			args: []string{"run", "--rm", "-i", "-v", workspace + ":/workspace:ro", "--volume", tmp + ":/payloads:rw", "image:latest"},
		},
		{
			name: "no mounts pass",
			args: []string{"run", "--rm", "-i", "-e", "NO_COLOR=1", "image:latest"},
		},
		{
			name:    "disallowed -v mount rejected",
			args:    []string{"run", "-v", "/etc:/host-etc:ro", "image:latest"},
			wantErr: "outside the allowed mount roots",
		},
		{
			name:    "disallowed --volume= mount rejected",
			args:    []string{"run", "--volume=/etc:/host-etc:ro", "image:latest"},
			wantErr: "outside the allowed mount roots",
		},
		{
			name:    "dangling -v rejected",
			args:    []string{"run", "-v"},
			wantErr: "missing mount declaration",
		},
		{
			name:    "--mount bypass rejected",
			args:    []string{"run", "--mount", "type=bind,source=/etc,target=/host-etc", "image:latest"},
			wantErr: "--mount",
		},
		{
			name:    "--volumes-from bypass rejected",
			args:    []string{"run", "--volumes-from", "other", "image:latest"},
			wantErr: "--volumes-from",
		},
		{
			name:    "--privileged bypass rejected",
			args:    []string{"run", "--privileged", "image:latest"},
			wantErr: "--privileged",
		},
		{
			name:    "--device bypass rejected",
			args:    []string{"run", "--device=/dev/sda:/dev/sda", "image:latest"},
			wantErr: "--device",
		},
		{
			name:    "Podman --rootfs bypass rejected",
			args:    []string{"run", "--rootfs=/etc", "image:latest"},
			wantErr: "--rootfs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := policy.ValidateContainerArgs(tt.args)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestLaunchStdioConnectionOnlyValidatesRuntimeArgs(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"entrypoint-option": {
			Type:                 "stdio",
			Containerized:        true,
			ContainerRuntimeArgs: []string{"run", "--rm"},
			Command:              "nonexistent-runtime-12345",
			Args:                 []string{"run", "--rm", "image:latest", "--privileged"},
		},
	})

	l := New(context.Background(), cfg)
	defer l.Close()

	_, err := GetOrLaunch(l, "entrypoint-option")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create connection")
	assert.NotContains(t, err.Error(), "mount policy")
}

func TestDefaultMountPolicyAllowsWorkspaceAndTemp(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("GITHUB_WORKSPACE", workspace)
	t.Setenv(AllowedMountRootsEnvVar, "")

	policy := DefaultMountPolicy()
	require.NotEmpty(t, policy.Roots)

	assert.NoError(t, policy.ValidateMount(workspace+":/workspace:ro"))
	assert.NoError(t, policy.ValidateMount(filepath.Join(os.TempDir(), "jq-payloads")+":/payloads:rw"))
	require.Error(t, policy.ValidateMount("/etc:/host-etc:ro"))
	require.Error(t, policy.ValidateMount(workspace+":/workspace:rw"))
}

func TestDefaultMountPolicyEnvOverride(t *testing.T) {
	allowed := t.TempDir()
	readonly := filepath.Join(allowed, "readonly")
	require.NoError(t, os.MkdirAll(readonly, 0o755))
	t.Setenv("GITHUB_WORKSPACE", "")
	t.Setenv(AllowedMountRootsEnvVar, allowed+":rw, relative/path, "+readonly+":ro")

	policy := DefaultMountPolicy()
	require.Len(t, policy.Roots, 2, "non-absolute roots are ignored")
	canonicalReadonly, err := filepath.EvalSymlinks(readonly)
	require.NoError(t, err)
	assert.Equal(t, canonicalReadonly, policy.Roots[0].Path, "more specific roots take precedence")

	assert.NoError(t, policy.ValidateMount(allowed+":/data:rw"))
	assert.NoError(t, policy.ValidateMount(readonly+":/data:ro"))
	require.Error(t, policy.ValidateMount(readonly+":/data:rw"), "read-only sub-root overrides writable parent")
	require.Error(t, policy.ValidateMount(os.TempDir()+":/data:rw"), "default roots do not apply when overridden")
}

func TestCanonicalizePathHandlesMissingLeafDirectories(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	require.NoError(t, os.MkdirAll(real, 0o755))
	link := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(real, link))

	got, err := canonicalizePath(filepath.Join(link, "missing", "child"))
	require.NoError(t, err)

	resolvedReal, err := filepath.EvalSymlinks(real)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(resolvedReal, "missing", "child"), got)

	_, err = canonicalizePath("relative/path")
	assert.Error(t, err)
}

// TestCanonicalizePathRejectsNonNotExistError covers the "!os.IsNotExist(err)"
// branch in canonicalizePath: when filepath.EvalSymlinks fails with an error
// other than "not exist" (e.g. ENOTDIR from treating a regular file as a
// directory component), canonicalizePath must surface a generic resolution
// error instead of silently walking further up the tree.
func TestCanonicalizePathRejectsNonNotExistError(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "not-a-directory")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))

	_, err := canonicalizePath(filepath.Join(file, "child"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot resolve path")
}

// TestValidateMountRejectsUncanonicalizableSource covers ValidateMount's
// canonicalizePath error branch: a source whose ancestor path component is a
// regular file (making resolution fail with a non-"not exist" error) must be
// rejected with a "cannot be canonicalized" message before any root matching
// is attempted.
func TestValidateMountRejectsUncanonicalizableSource(t *testing.T) {
	policy, _, tmp := newTestPolicy(t)
	file := filepath.Join(tmp, "not-a-directory")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))

	err := policy.ValidateMount(filepath.Join(file, "child") + ":/dest:ro")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be canonicalized")
}

// TestLaunchStdioConnectionEnforcesMountPolicy verifies that mount policy
// violations are rejected before the backend process is launched.
func TestLaunchStdioConnectionEnforcesMountPolicy(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"bad-mount": {
			Type:          "stdio",
			Containerized: true,
			Command:       "docker",
			Args:          []string{"run", "--rm", "-i", "-v", "/etc:/host-etc:ro", "image:latest"},
		},
	})

	l := New(context.Background(), cfg)
	defer l.Close()

	conn, err := GetOrLaunch(l, "bad-mount")
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.Contains(t, err.Error(), "outside the allowed mount roots")
	assert.NotContains(t, err.Error(), "failed to create connection", "launch must be blocked before process start")
}

// TestLaunchStdioConnectionSkipsPolicyForDirectCommands verifies that
// non-container stdio commands are not subject to container mount validation.
func TestLaunchStdioConnectionSkipsPolicyForDirectCommands(t *testing.T) {
	cfg := newTestConfig(map[string]*config.ServerConfig{
		"direct": {
			Type:    "stdio",
			Command: "nonexistent-command-12345",
			Args:    []string{"-v", "/etc:/host-etc:ro"},
		},
	})

	l := New(context.Background(), cfg)
	defer l.Close()

	_, err := GetOrLaunch(l, "direct")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create connection")
}

func TestCanonicalizeRootsRejectsFilesystemRoot(t *testing.T) {
	roots := canonicalizeRoots([]MountRoot{{Path: "/", Writable: true}, {Path: os.TempDir(), Writable: true}})
	require.Len(t, roots, 1)
	assert.NotEqual(t, "/", roots[0].Path)
}

func TestValidateMountRejectsConflictingModes(t *testing.T) {
	err := (MountPolicy{}).ValidateMount("/srv/data:/data:ro,rw")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicting mount options")
}

// TestCanonicalizeRootsDropsUncanonicalizableRoots covers the branch in
// canonicalizeRoots where canonicalizePath fails (non-absolute root path):
// the root must be silently dropped rather than causing a panic or being
// included in the resulting allowlist.
func TestCanonicalizeRootsDropsUncanonicalizableRoots(t *testing.T) {
	tmp := t.TempDir()

	roots := canonicalizeRoots([]MountRoot{
		{Path: "relative/not-absolute", Writable: true},
		{Path: tmp, Writable: false},
	})

	require.Len(t, roots, 1, "the non-absolute root must be dropped, leaving only the valid one")
	canonicalTmp, err := filepath.EvalSymlinks(tmp)
	require.NoError(t, err)
	assert.Equal(t, canonicalTmp, roots[0].Path)
	assert.False(t, roots[0].Writable)
}

// TestCanonicalizeRootsDeduplicatesEquivalentPaths covers the "seen[path]"
// dedup branch: two distinct MountRoot entries that canonicalize to the same
// underlying path (e.g. via a symlink) must collapse into a single root,
// keeping the writability of whichever entry was seen first.
func TestCanonicalizeRootsDeduplicatesEquivalentPaths(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	require.NoError(t, os.MkdirAll(real, 0o755))
	link := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(real, link))

	roots := canonicalizeRoots([]MountRoot{
		{Path: real, Writable: false},
		{Path: link, Writable: true},
	})

	require.Len(t, roots, 1, "real path and its symlink alias must dedupe to one root")
	canonicalReal, err := filepath.EvalSymlinks(real)
	require.NoError(t, err)
	assert.Equal(t, canonicalReal, roots[0].Path)
	assert.False(t, roots[0].Writable, "the first-seen entry's writability wins")
}

// TestCanonicalizeRootsEmptyInput covers the zero-iteration loop case: an
// empty input slice must yield an empty (not nil-panicking) result.
func TestCanonicalizeRootsEmptyInput(t *testing.T) {
	roots := canonicalizeRoots(nil)
	assert.Empty(t, roots)
}

// TestParseMountRootsSkipsEmptyEntries covers the empty-entry continue branch
// in parseMountRoots: blank entries from stray/trailing commas must be
// silently skipped rather than producing a malformed root.
func TestParseMountRootsSkipsEmptyEntries(t *testing.T) {
	allowed := t.TempDir()

	policy := parseMountRoots(allowed + ":rw,, ,")
	require.Len(t, policy.Roots, 1, "blank entries must be skipped")
	canonicalAllowed, err := filepath.EvalSymlinks(allowed)
	require.NoError(t, err)
	assert.Equal(t, canonicalAllowed, policy.Roots[0].Path)
	assert.True(t, policy.Roots[0].Writable)
}

// TestCanonicalizePathWalksUpToFilesystemRoot covers the ancestor-walk loop in
// canonicalizePath when no component of the path exists: traversal continues
// until the filesystem root, which always resolves, and the missing components
// are appended to it. The parent == current branch is unreachable on a real
// filesystem because "/" always exists.
func TestCanonicalizePathWalksUpToFilesystemRoot(t *testing.T) {
	missing := filepath.Join(string(filepath.Separator), "gh-aw-mcpg-missing-root-8f2c", "nested", "leaf")
	_, statErr := os.Lstat(filepath.Dir(filepath.Dir(missing)))
	require.True(t, os.IsNotExist(statErr), "test requires a top-level path that does not exist")

	got, err := canonicalizePath(missing)
	require.NoError(t, err)
	assert.Equal(t, missing, got)
}

// TestValidateMountRejectsEmptyModeOption verifies a stray comma within the mode
// segment is rejected by the shared mount parser.
func TestValidateMountRejectsEmptyModeOption(t *testing.T) {
	err := (MountPolicy{}).ValidateMount("/srv/data:/data:ro,,rw")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty mount option")
}

// TestIsUnderRoot directly exercises isUnderRoot's branches: exact match,
// nested path, sibling path with a shared prefix (must not be treated as
// "under" merely due to string prefix matching), parent traversal escape,
// and the filepath.Rel error branch triggered by mixing an absolute path
// with a relative root (or vice versa).
func TestIsUnderRoot(t *testing.T) {
	tests := []struct {
		name string
		path string
		root string
		want bool
	}{
		{
			name: "path equals root",
			path: "/data/workspace",
			root: "/data/workspace",
			want: true,
		},
		{
			name: "path nested under root",
			path: "/data/workspace/repo/file.go",
			root: "/data/workspace",
			want: true,
		},
		{
			name: "sibling path sharing string prefix is not under root",
			path: "/data/workspace-other",
			root: "/data/workspace",
			want: false,
		},
		{
			name: "path escapes root via parent traversal",
			path: "/data/etc",
			root: "/data/workspace",
			want: false,
		},
		{
			name: "path is parent of root path",
			path: "/",
			root: "/data/workspace",
			want: false,
		},
		{
			name: "filepath.Rel error from mixing absolute path and relative root",
			path: "/data/workspace",
			root: "relative/root",
			want: false,
		},
		{
			name: "filepath.Rel error from mixing relative path and absolute root",
			path: "relative/path",
			root: "/data/workspace",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUnderRoot(tt.path, tt.root)
			assert.Equal(t, tt.want, got)
		})
	}
}
