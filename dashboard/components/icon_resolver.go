package components

import (
	"github.com/a-h/templ"
	"github.com/xraph/forgeui/icons"
)

// ResolveIcon returns the Lucide icon component for the given icon name.
func ResolveIcon(iconName string, opts ...icons.Option) templ.Component {
	switch iconName {
	case "layout-dashboard":
		return icons.LayoutDashboard(opts...)
	case "server":
		return icons.Server(opts...)
	case "rocket":
		return icons.Rocket(opts...)
	case "heart-pulse":
		return icons.HeartPulse(opts...)
	case "globe":
		return icons.Globe(opts...)
	case "key-round":
		return icons.KeyRound(opts...)
	case "building-2":
		return icons.Building2(opts...)
	case "scroll-text":
		return icons.ScrollText(opts...)
	case "cloud":
		return icons.Cloud(opts...)
	case "activity":
		return icons.Activity(opts...)
	case "users":
		return icons.Users(opts...)
	case "shield":
		return icons.Shield(opts...)
	case "check-circle":
		return icons.CheckCircle(opts...)
	case "x-circle":
		return icons.XCircle(opts...)
	case "alert-triangle":
		return icons.TriangleAlert(opts...)
	case "help-circle", "circle-help":
		return icons.CircleQuestionMark(opts...)
	case "circle-dot":
		return icons.CircleDot(opts...)
	case "play":
		return icons.Play(opts...)
	case "square":
		return icons.Square(opts...)
	case "rotate-ccw":
		return icons.RotateCcw(opts...)
	case "trash-2":
		return icons.Trash2(opts...)
	case "pause":
		return icons.Pause(opts...)
	case "chevron-left":
		return icons.ChevronLeft(opts...)
	case "monitor":
		return icons.Monitor(opts...)
	case "cpu":
		return icons.Cpu(opts...)
	case "hard-drive":
		return icons.HardDrive(opts...)
	case "network":
		return icons.Network(opts...)
	case "lock":
		return icons.Lock(opts...)
	case "unlock":
		return icons.LockOpen(opts...)
	case "calendar":
		return icons.Calendar(opts...)
	case "clock":
		return icons.Clock(opts...)
	case "hash":
		return icons.Hash(opts...)
	case "tag":
		return icons.Tag(opts...)
	case "link":
		return icons.Link(opts...)
	case "trending-up":
		return icons.TrendingUp(opts...)
	case "trending-down":
		return icons.TrendingDown(opts...)
	case "zap":
		return icons.Zap(opts...)
	case "database":
		return icons.Database(opts...)
	case "settings":
		return icons.Settings(opts...)
	case "file-text":
		return icons.FileText(opts...)
	case "info":
		return icons.Info(opts...)
	case "memory-stick":
		return icons.MemoryStick(opts...)
	case "bar-chart-3":
		return icons.ChartBar(opts...)
	case "gauge":
		return icons.Gauge(opts...)
	case "bell":
		return icons.Bell(opts...)
	case "timer":
		return icons.Timer(opts...)
	case "alert-circle":
		return icons.AlertCircle(opts...)
	default:
		return icons.Info(opts...)
	}
}
