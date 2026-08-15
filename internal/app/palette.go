package app

import "github.com/charmbracelet/lipgloss"

// Palette · low-saturation, cold-gray base + soft blue accent.
const (
	bgColor        = "#141621"
	surfaceColor   = "#1B1E2B"
	selectionColor = "#282D42"
	textColor      = "#C4CBE3"
	mutedColor     = "#68708C"
	ruleColor      = "#3E4E78"
	accentColor    = "#7FA9FF"
	greenColor     = "#9CCB65"
	warningColor   = "#E5B76B"
	dangerColor    = "#EF7996"
)

// surfaceRGB mirrors surfaceColor for true-color termansi backgrounds.
const surfaceRGB = 0x1B1E2B

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
