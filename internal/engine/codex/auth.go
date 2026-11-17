package codex

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// SignedIn asks Codex whether it is logged in, in the environment its sessions get.
// `codex login status` exits 1 with "Not logged in" when it isn't.
func (Engine) SignedIn(ctx context.Context, bin string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "login", "status")
	cmd.Env = childEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	if strings.Contains(strings.ToLower(string(out)), "not logged in") {
		return false, nil
	}
	return false, fmt.Errorf("codex login status: %w", err)
}
