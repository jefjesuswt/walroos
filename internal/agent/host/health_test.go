package host

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModulesGone(t *testing.T) {
	usr, lib := t.TempDir(), filepath.Join(t.TempDir(), "missing")
	os.Mkdir(filepath.Join(usr, "7.3.5-zen1"), 0o755)
	if !modulesGone("7.3.4-zen2", usr, lib) {
		t.Error("only a newer kernel's modules: updated, not rebooted")
	}
	if modulesGone("7.3.5-zen1", usr, lib) {
		t.Error("the running kernel's modules are there")
	}
	if modulesGone("7.3.4-zen2", t.TempDir(), lib) {
		t.Error("no modules at all (a container, WSL): can't tell, so no problem")
	}
}

func TestHasDefaultRoute(t *testing.T) {
	head := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"
	if !hasDefaultRoute(head + "enp42s0\t00000000\t0100A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n") {
		t.Error("a default route")
	}
	if hasDefaultRoute(head + "podman4\t0003590A\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n") {
		t.Error("only the bridge's own subnet: no way out")
	}
}
