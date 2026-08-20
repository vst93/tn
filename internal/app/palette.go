package app

import "github.com/charmbracelet/lipgloss"

// Default palette · low-saturation, cold-gray base + soft blue accent.
// These are vars (not const) so themes can swap them at runtime.
var (
	bgColor        = "#141621"
	surfaceColor   = "#1B1E2B"
	selectionColor = "#282D42"
	textColor      = "#C4CBE3"
	mutedColor     = "#68708C"
	ruleColor      = "#333A57"
	accentColor    = "#7FA9FF"
	greenColor     = "#9CCB65"
	warningColor   = "#E5B76B"
	dangerColor    = "#EF7996"
)

// surfaceRGB mirrors surfaceColor for true-color termansi backgrounds.
var surfaceRGB = 0x1B1E2B

var (
	bg        = lipgloss.Color(bgColor)
	surface   = lipgloss.Color(surfaceColor)
	selection = lipgloss.Color(selectionColor)
	text      = lipgloss.Color(textColor)
	muted     = lipgloss.Color(mutedColor)
	rule      = lipgloss.Color(ruleColor)
	accent    = lipgloss.Color(accentColor)
	green     = lipgloss.Color(greenColor)
	warning   = lipgloss.Color(warningColor)
	danger    = lipgloss.Color(dangerColor)

	headerSty = lipgloss.NewStyle().Foreground(text).Bold(true)
	brandSty  = lipgloss.NewStyle().Foreground(text).Bold(true)
	mutedSty  = lipgloss.NewStyle().Foreground(muted)
	statusSty = lipgloss.NewStyle().Foreground(muted)
	errorSty  = lipgloss.NewStyle().Foreground(danger).Bold(true)
)

// rebuildStyles rebuilds the package-level lipgloss colors and styles from
// the current color-string variables. Call after changing a theme.
func rebuildStyles() {
	bg = lipgloss.Color(bgColor)
	surface = lipgloss.Color(surfaceColor)
	selection = lipgloss.Color(selectionColor)
	text = lipgloss.Color(textColor)
	muted = lipgloss.Color(mutedColor)
	rule = lipgloss.Color(ruleColor)
	accent = lipgloss.Color(accentColor)
	green = lipgloss.Color(greenColor)
	warning = lipgloss.Color(warningColor)
	danger = lipgloss.Color(dangerColor)

	headerSty = lipgloss.NewStyle().Foreground(text).Bold(true)
	brandSty = lipgloss.NewStyle().Foreground(text).Bold(true)
	mutedSty = lipgloss.NewStyle().Foreground(muted)
	statusSty = lipgloss.NewStyle().Foreground(muted)
	errorSty = lipgloss.NewStyle().Foreground(danger).Bold(true)
}
