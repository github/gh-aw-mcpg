package githubhttp

import (
	"context"
	"fmt"
)

func VerifySinkVisibility(ctx context.Context, apiBaseURL, nwo, authHeader, configuredVisibility string) (string, bool, error) {
	if nwo == "" {
		return configuredVisibility, false, fmt.Errorf("no repository configured for sink visibility verification")
	}
	actual, err := FetchRepoVisibility(ctx, apiBaseURL, nwo, authHeader)
	if err != nil {
		return configuredVisibility, false, err
	}
	effective, overridden := EffectiveSinkVisibility(configuredVisibility, actual)
	return effective, overridden, nil
}
