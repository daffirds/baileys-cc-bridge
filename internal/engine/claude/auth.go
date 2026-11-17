package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/daffirds/baileys-cc-bridge/internal/engine"
)

// SignedIn asks Claude Code whether it is logged in, in the environment its sessions get.
// `claude auth status` prints JSON either way and exits 1 when logged out.
func (Engine) SignedIn(ctx context.Context, bin string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "auth", "status")
	cmd.Dir = os.TempDir()
	cmd.Env = childEnv(engine.Spec{Workdir: cmd.Dir})
	out, err := cmd.Output()
	var st struct {
		LoggedIn bool `json:"loggedIn"`
	}
	if jerr := json.Unmarshal(out, &st); jerr != nil {
		if err == nil {
			err = jerr
		}
		return false, fmt.Errorf("claude auth status: %w", err)
	}
	return st.LoggedIn, nil
}
