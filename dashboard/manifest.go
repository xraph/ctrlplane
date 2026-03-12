package dashboard

import (
	"github.com/xraph/forge/extensions/dashboard/contributor"
)

// NewManifest builds a contributor.Manifest for the ctrlplane dashboard.
func NewManifest() *contributor.Manifest {
	return &contributor.Manifest{
		Name:        "ctrlplane",
		DisplayName: "CtrlPlane",
		Icon:        "cloud",
		Version:     "0.1.0",
		Layout:      "extension",
		ShowSidebar: boolPtr(true),
		Nav:         buildNav(),
		Widgets:     buildWidgets(),
		Settings:    buildSettings(),
	}
}

func buildNav() []contributor.NavItem {
	return []contributor.NavItem{
		{Label: "Overview", Path: "/", Icon: "layout-dashboard", Group: "CtrlPlane", Priority: 0},
		{Label: "Instances", Path: "/instances", Icon: "server", Group: "Infrastructure", Priority: 0},
		{Label: "Deployments", Path: "/deployments", Icon: "rocket", Group: "Infrastructure", Priority: 1},
		{Label: "Health", Path: "/health", Icon: "heart-pulse", Group: "Infrastructure", Priority: 2},
		{Label: "Providers", Path: "/providers", Icon: "cloud", Group: "Infrastructure", Priority: 3},
		{Label: "Workers", Path: "/workers", Icon: "settings", Group: "Infrastructure", Priority: 4},
		{Label: "Events", Path: "/events", Icon: "bell", Group: "Infrastructure", Priority: 5},
		{Label: "Templates", Path: "/templates", Icon: "file-text", Group: "Infrastructure", Priority: 6},
		{Label: "Datacenters", Path: "/datacenters", Icon: "map-pin", Group: "Infrastructure", Priority: 7},
		{Label: "Network", Path: "/network", Icon: "globe", Group: "Networking", Priority: 0},
		{Label: "Secrets", Path: "/secrets", Icon: "key-round", Group: "Networking", Priority: 1},
		{Label: "Tenants", Path: "/tenants", Icon: "building-2", Group: "Administration", Priority: 0},
		{Label: "Audit Log", Path: "/audit", Icon: "scroll-text", Group: "Administration", Priority: 1},
	}
}

func buildWidgets() []contributor.WidgetDescriptor {
	return []contributor.WidgetDescriptor{
		{
			ID:          "ctrlplane-system-stats",
			Title:       "System Overview",
			Description: "Tenants, instances, and provider counts.",
			Size:        "md",
			RefreshSec:  60,
			Group:       "CtrlPlane",
		},
		{
			ID:          "ctrlplane-recent-deploys",
			Title:       "Recent Deployments",
			Description: "Latest deployment activity.",
			Size:        "md",
			RefreshSec:  30,
			Group:       "CtrlPlane",
		},
		{
			ID:          "ctrlplane-health-summary",
			Title:       "Health Summary",
			Description: "Aggregate health status across instances.",
			Size:        "md",
			RefreshSec:  30,
			Group:       "CtrlPlane",
		},
		{
			ID:          "ctrlplane-workers",
			Title:       "Workers",
			Description: "Background worker status overview.",
			Size:        "sm",
			RefreshSec:  30,
			Group:       "CtrlPlane",
		},
	}
}

func buildSettings() []contributor.SettingsDescriptor {
	return []contributor.SettingsDescriptor{
		{
			ID:          "ctrlplane-config",
			Title:       "CtrlPlane Settings",
			Description: "View control plane configuration and status.",
			Group:       "CtrlPlane",
			Icon:        "cloud",
		},
	}
}
