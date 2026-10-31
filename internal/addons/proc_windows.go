package addons

import "os/exec"

func setProcGroup(*exec.Cmd) {}

// No SIGTERM on Windows: stdin closing was the polite request, this is the end of it.
func terminate(cmd *exec.Cmd) { _ = cmd.Process.Kill() }

func killGroup(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
