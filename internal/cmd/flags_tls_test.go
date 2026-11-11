package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTLSCmd builds a command with TLS flags registered, mirroring
// how rootCmd is initialized in production.
func setupTLSCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{
		Use:          "test",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	// Register TLS flags (and the MarkFlagsRequiredTogether constraint).
	cmd.Flags().StringVar(new(string), "tls-cert", "", "Path to TLS cert")
	cmd.Flags().StringVar(new(string), "tls-key", "", "Path to TLS key")
	cmd.Flags().StringVar(new(string), "tls-ca", "", "Path to CA cert")
	cmd.MarkFlagsRequiredTogether("tls-cert", "tls-key")
	return cmd
}

// TestTLSFlagsRequiredTogether verifies that cobra's MarkFlagsRequiredTogether
// constraint on --tls-cert and --tls-key is wired up correctly.
func TestTLSFlagsRequiredTogether(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		errContains []string
	}{
		{"cert without key is rejected", []string{"--tls-cert", "/tmp/cert.pem"}, true, []string{"tls-cert", "tls-key"}},
		{"key without cert is rejected", []string{"--tls-key", "/tmp/key.pem"}, true, []string{"tls-cert", "tls-key"}},
		{"ca alone is accepted", []string{"--tls-ca", "/tmp/ca.pem"}, false, nil},
		{"cert and key together are accepted", []string{"--tls-cert", "/tmp/cert.pem", "--tls-key", "/tmp/key.pem"}, false, nil},
		{"neither cert nor key is accepted (TLS disabled)", []string{}, false, nil},
		{"cert key and ca together are accepted", []string{"--tls-cert", "/tmp/cert.pem", "--tls-key", "/tmp/key.pem", "--tls-ca", "/tmp/ca.pem"}, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := setupTLSCmd(t)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, s := range tt.errContains {
				assert.Contains(t, err.Error(), s)
			}
		})
	}
}

// TestTLSFlagsRegistered verifies that the TLS flags exist on the root command.
func TestTLSFlagsRegistered(t *testing.T) {
	for _, name := range []string{"tls-cert", "tls-key", "tls-ca", "hmac-secret"} {
		t.Run(name, func(t *testing.T) {
			f := rootCmd.Flags().Lookup(name)
			require.NotNil(t, f, "%s flag should be registered on rootCmd", name)
			assert.Equal(t, "string", f.Value.Type())
		})
	}
}
