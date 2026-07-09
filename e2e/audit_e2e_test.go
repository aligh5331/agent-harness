package e2e

import (
	"os/exec"
	"testing"
)

func TestAuditCapability(t *testing.T) {
	t.Run("AuditPhase", func(t *testing.T) {
		cmd := exec.Command("../harness", "--audit-phase", "6")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("audit-phase failed: %v, output: %s", err, string(out))
		}
	})

	t.Run("AuditFull", func(t *testing.T) {
		cmd := exec.Command("../harness", "--audit-full")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("audit-full failed: %v, output: %s", err, string(out))
		}
	})
}
