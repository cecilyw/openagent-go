package components

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// RenderLogoColored applies color to a multi-line logo string. When gradient
// has 2+ stops, a vertical gradient is interpolated across the lines (top
// stop → bottom stop) via lipgloss.Blend1D and each line gets its own
// foreground. With a single stop, every line uses that color. Returns the
// art unchanged if neither gradient nor singleColor is set (the caller then
// applies its own default).
//
// All lines are first padded to equal width so that downstream Center/Width
// alignment (which pads per-line) doesn't shift glyphs out of column.
func RenderLogoColored(art, singleColor string, gradient []string) string {
	lines := strings.Split(art, "\n")
	if len(lines) == 0 {
		return art
	}

	// Pad all lines to the same visible width.
	maxW := 0
	for _, ln := range lines {
		if w := lipgloss.Width(ln); w > maxW {
			maxW = w
		}
	}
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w < maxW {
			lines[i] = ln + strings.Repeat(" ", maxW-w)
		}
	}

	// Collect non-empty gradient stops.
	var stops []color.Color
	for _, hex := range gradient {
		hex = strings.TrimSpace(hex)
		if hex != "" {
			stops = append(stops, lipgloss.Color(hex))
		}
	}

	// Gradient path: 2+ stops → per-line interpolated color.
	if len(stops) >= 2 {
		colors := lipgloss.Blend1D(len(lines), stops...)
		out := make([]string, len(lines))
		for i, ln := range lines {
			out[i] = lipgloss.NewStyle().Foreground(colors[i]).Render(ln)
		}
		return strings.Join(out, "\n")
	}

	// Single-color path: 1 gradient stop, or the singleColor field.
	if len(stops) == 1 {
		return renderSingleColor(lines, stops[0])
	}
	if singleColor = strings.TrimSpace(singleColor); singleColor != "" {
		return renderSingleColor(lines, lipgloss.Color(singleColor))
	}
	return strings.Join(lines, "\n")
}

func renderSingleColor(lines []string, c color.Color) string {
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = lipgloss.NewStyle().Foreground(c).Render(ln)
	}
	return strings.Join(out, "\n")
}
