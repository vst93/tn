package app

import "strconv"

// Theme is a named set of colors.
type Theme struct {
	Name      string
	BG        string
	Surface   string
	Selection string
	Text      string
	Muted     string
	Rule      string
	Accent    string
	Green     string
	Warning   string
	Danger    string
}

// Built-in themes. Index 0 is the default.
var themeList = []Theme{
	{
		Name:      "Midnight",
		BG:        "#141621",
		Surface:   "#1B1E2B",
		Selection: "#282D42",
		Text:      "#C4CBE3",
		Muted:     "#68708C",
		Rule:      "#333A57",
		Accent:    "#7FA9FF",
		Green:     "#9CCB65",
		Warning:   "#E5B76B",
		Danger:    "#EF7996",
	},
	{
		Name:      "Paper",
		BG:        "#F5F5F0",
		Surface:   "#FFFFFF",
		Selection: "#D4D4CC",
		Text:      "#2A2A2A",
		Muted:     "#7A7A7A",
		Rule:      "#C0C0B8",
		Accent:    "#3366CC",
		Green:     "#3D7A3D",
		Warning:   "#A07020",
		Danger:    "#C03030",
	},
	{
		Name:      "Forest",
		BG:        "#0F1A14",
		Surface:   "#16241B",
		Selection: "#1E3828",
		Text:      "#B8D4C4",
		Muted:     "#6A8070",
		Rule:      "#2D4A38",
		Accent:    "#5FB878",
		Green:     "#85C99F",
		Warning:   "#D4B060",
		Danger:    "#E07070",
	},
}

// activeThemeIndex tracks which theme is currently applied.
var activeThemeIndex = 0

// currentTheme returns the active theme.
func currentTheme() Theme {
	return themeList[activeThemeIndex]
}

// applyTheme sets the palette colors from the given theme and rebuilds styles.
func applyTheme(t Theme) {
	bgColor = t.BG
	surfaceColor = t.Surface
	selectionColor = t.Selection
	textColor = t.Text
	mutedColor = t.Muted
	ruleColor = t.Rule
	accentColor = t.Accent
	greenColor = t.Green
	warningColor = t.Warning
	dangerColor = t.Danger

	surfaceRGB = hexToRGB(t.Surface)
	rebuildStyles()
}

// nextTheme switches to the next theme in the list and applies it.
func nextTheme() {
	activeThemeIndex = (activeThemeIndex + 1) % len(themeList)
	applyTheme(currentTheme())
}

// setThemeByName selects a theme by name. Returns false if not found.
func setThemeByName(name string) bool {
	for i, t := range themeList {
		if t.Name == name {
			activeThemeIndex = i
			applyTheme(t)
			return true
		}
	}
	return false
}

// hexToRGB converts "#RRGGBB" into an int for true-color backgrounds.
func hexToRGB(h string) int {
	h = trimPrefix(h, "#")
	if len(h) != 6 {
		return 0
	}
	n, _ := strconv.ParseInt(h, 16, 32)
	return int(n)
}

func trimPrefix(s, prefix string) string {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}
