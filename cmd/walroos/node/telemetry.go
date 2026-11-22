package node

import (
	"context"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	podman "github.com/jefjesuswt/walroos/internal/agent/host"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/version"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// collectTelemetry is best effort: a failing probe leaves its fields zero.
func collectTelemetry(ctx context.Context) fleet.Telemetry {
	t := fleet.Telemetry{OS: runtime.GOOS, Arch: runtime.GOARCH, Walroos: version.Version, Containers: -1}
	t.Hostname, _ = os.Hostname()

	if info, err := host.InfoWithContext(ctx); err == nil && info.Platform != "" {
		t.OS = strings.TrimSpace(info.Platform + " " + info.PlatformVersion) // e.g. "debian 13.1"; rolling distros like Arch have no version
	}
	if p, err := cpu.PercentWithContext(ctx, time.Second, false); err == nil && len(p) > 0 {
		t.CPUPercent = math.Round(p[0]*10) / 10
	}
	if m, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		t.MemUsedMB, t.MemTotalMB = m.Used>>20, m.Total>>20
	}
	if d, err := disk.UsageWithContext(ctx, "/"); err == nil {
		t.DiskUsedGB, t.DiskTotalGB = d.Used>>30, d.Total>>30
	}

	t.Podman = podman.PodmanVersion(ctx)
	t.Problems = podman.Problems(ctx)
	if out, err := exec.CommandContext(ctx, "podman", "ps", "-aq").Output(); err == nil {
		t.Containers = len(strings.Fields(string(out)))
	}
	return t
}
