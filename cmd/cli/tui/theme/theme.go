package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Package theme holds the TUI color palette and base styles, ported verbatim
// from the /tmp/tui reference (huawei.com/aicli) so the welcome and chat pages
// render with the same visual identity.

var (
	// Color Design
	BgNormal    = lipgloss.Color("#0a0a0a") // 整体背景
	BgSecondary = lipgloss.Color("#141414") // 右侧面板
	BgSurface   = lipgloss.Color("#202021") // 输入框
	BgGray      = lipgloss.Color("#888B7E")

	BorderGray = lipgloss.Color("#484848")

	TextNormal   = lipgloss.Color("#fdfcfc") // 用户文本，标题
	TextAsh      = lipgloss.Color("#9a9898") // 输出文本
	TextStone    = lipgloss.Color("#6e6e73") // 思考文本
	TextMute     = lipgloss.Color("#646262")
	TextBody     = lipgloss.Color("#424245")
	TextCharcoal = lipgloss.Color("#302c2c")
	TextInk      = lipgloss.Color("#201d1d")

	Primary = lipgloss.Color("#007aff")
	Danger  = lipgloss.Color("#ff3b30")
	Success = lipgloss.Color("#30d158")
	Warning = lipgloss.Color("#ff9f0a")

	WarningHover  = lipgloss.Color("#cc7f08")
	WarningActive = lipgloss.Color("#995f06")

	// 滚动条颜色
	ThumbBackGround = lipgloss.Color("#484848")
	TrackBackGround = BgSecondary

	ThumbBackGroundActive = lipgloss.Color("#545454")
	TrackBackGroundActive = BgSurface
	ThumbBackGroundDrag   = lipgloss.Color("#666666")

	// 命令的颜色
	CommandActive   = lipgloss.Color("#fab283")
	CommandInactive = lipgloss.Color("#995f06")
)

func BaseStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(BgNormal).Foreground(TextNormal)
}

func ButtonStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(BgGray).
		Foreground(TextNormal).
		Padding(0, 1).
		MarginBackground(BgNormal) // 需要设置外边距的背景颜色
}

func ActiveButtonStyle() lipgloss.Style {
	return ButtonStyle().
		Background(Primary).
		Foreground(TextNormal).
		Underline(true)
}

func HelpLabel() lipgloss.Style {
	return lipgloss.NewStyle().Background(BgNormal).Foreground(TextStone).Padding(0, 1)
}

// ApplyOverrides merges hex color overrides onto the package-level palette
// vars. Keys map to the config.TUIColors JSON field names; an empty value
// (or absent key) keeps the built-in default. Call once at TUI startup,
// before any BaseStyle() call, so the overridden vars are picked up.
//
// Accepted keys: bg_normal, bg_secondary, bg_surface, primary, success,
// warning, danger, text_normal, text_ash, border_gray.
func ApplyOverrides(overrides map[string]string) {
	for key, val := range overrides {
		if val == "" {
			continue
		}
		c := lipgloss.Color(val)
		switch key {
		case "bg_normal":
			BgNormal = c
		case "bg_secondary":
			BgSecondary = c
		case "bg_surface":
			BgSurface = c
		case "primary":
			Primary = c
		case "success":
			Success = c
		case "warning":
			Warning = c
		case "danger":
			Danger = c
		case "text_normal":
			TextNormal = c
		case "text_ash":
			TextAsh = c
		case "border_gray":
			BorderGray = c
		}
	}
}

// colorVar is the element type of the palette vars (color.Color), kept as
// a compile-time assertion that overrides target the right type.
var _ color.Color = BgNormal
