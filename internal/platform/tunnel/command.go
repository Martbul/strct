//go:build !test

// command_real.go provides the real os/exec implementation of newCommand.
// The test build tag allows command_test.go to replace this with a fake
// that doesn't require a real binary on disk.
//
// This file is the production path — it is compiled in all non-test builds.

package tunnel

import (
	"context"
	"os"
	"os/exec"
)

// newCommand returns a real *exec.Cmd that will be killed when ctx is cancelled.
// stdout and stderr are wired to the parent process so frpc output is visible in logs.
func newCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}
