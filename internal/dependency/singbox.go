package dependency

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Version reports the configured sing-box core version. ProxyLens deliberately
// does not download, replace, or otherwise manage the core executable.
func Version(ctx context.Context, path string) (string, error) {
	output, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return "", err
	}
	return parseVersion(string(output))
}

func parseVersion(output string) (string, error) {
	fields := strings.Fields(output)
	for i := range fields {
		if fields[i] == "version" && i+1 < len(fields) {
			return strings.TrimPrefix(fields[i+1], "v"), nil
		}
	}
	return "", errors.New("cannot parse sing-box version")
}
