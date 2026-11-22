package agent

// Problem is something wrong with the node that walroos can't fix by itself: what it breaks and
// the fix, for the node's page.
type Problem struct {
	Code  string `json:"code"`
	Title string `json:"title"`
	Fix   string `json:"fix"`
}

var (
	// KernelUpdated: the package manager replaced the running kernel's modules. Modules that
	// weren't loaded yet (nft_reject_inet, which the egress guard needs) can't load any more,
	// so every new app release fails to start until the node reboots.
	KernelUpdated = Problem{Code: "kernel_updated", Title: "The kernel was updated and the node hasn't rebooted",
		Fix: "Reboot the node. Until then new app releases can't start: their egress guard needs modules of the running kernel, which are gone."}
	// NoEgress: the rootless network namespace lost its way out (its pasta died, often after a
	// network change such as a VPN reconnecting). Containers on bridge networks have no internet
	// or DNS: the Cloudflare tunnel restarts in a loop, apps can't call out, workspaces can't
	// download. Builds still work: they get their own network.
	NoEgress = Problem{Code: "no_egress", Title: "Containers on this node have no internet",
		Fix: "Stop every container on a bridge network (podman ps shows them), then start them again: podman recreates the rootless network with a new way out. Stopping them cuts what they serve for a moment."}
)
