package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m *Model) defaultBackupPath() string {
	ts := time.Now().Format("20060102-150405")
	return filepath.Join(filepath.Dir(m.store.Root), fmt.Sprintf("tn-backup-%s.zip", ts))
}

func (m *Model) startBackup() {
	m.mode = modeBackup
	m.statusErr = false
	m.status = ""
	m.input.Prompt = "Backup to: "
	m.input.Placeholder = "path/to/backup.zip"
	m.input.SetValue(m.defaultBackupPath())
	m.input.Width = max(20, min(60, m.width-12))
	m.input.Focus()
}

func (m *Model) startImport() {
	m.mode = modeImport
	m.statusErr = false
	m.status = ""
	m.input.Prompt = "Import from: "
	m.input.Placeholder = "path/to/backup.zip"
	m.input.SetValue("")
	m.input.Width = max(20, min(60, m.width-12))
	m.input.Focus()
}

func (m *Model) updateBackup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		m.input.Prompt = "› "
		return m, m.takePending()
	case "enter":
		path := strings.TrimSpace(m.input.Value())
		if path == "" {
			m.setStatus("Enter a path", true)
			return m, nil
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(m.store.Root, path)
		}
		if err := m.exportBackup(path); err != nil {
			m.flashStatus("Backup failed: "+err.Error(), true, 3*time.Second)
		} else {
			m.flashStatus("✓ Backup saved to "+path, false, 3*time.Second)
		}
		m.mode = modeNormal
		m.input.Blur()
		m.input.Prompt = "› "
		return m, m.takePending()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) updateImport(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		m.input.Prompt = "› "
		return m, m.takePending()
	case "enter":
		path := strings.TrimSpace(m.input.Value())
		if path == "" {
			m.setStatus("Enter a path", true)
			return m, nil
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(m.store.Root, path)
		}
		count, err := m.importBackup(path)
		if err != nil {
			m.flashStatus("Import failed: "+err.Error(), true, 3*time.Second)
		} else {
			m.flashStatus(fmt.Sprintf("✓ Imported %d notes", count), false, 3*time.Second)
			m.refresh(m.selectedPath())
		}
		m.mode = modeNormal
		m.input.Blur()
		m.input.Prompt = "› "
		return m, m.takePending()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) backupView() string {
	var b strings.Builder
	b.WriteString(brandSty.Render("Backup notebook") + "\n\n")
	stats := m.collectBackupStats()
	b.WriteString(fmt.Sprintf("  Notes: %d\n", stats.NoteCount))
	b.WriteString(fmt.Sprintf("  Folders: %d\n", stats.FolderCount))
	b.WriteString(fmt.Sprintf("  Images: %d\n", stats.ImageCount))
	b.WriteString(fmt.Sprintf("  Words: %s\n", formatNum(stats.TotalWords)))
	b.WriteString(fmt.Sprintf("  Characters: %s\n", formatNum(stats.TotalChars)))
	b.WriteString("\n" + m.input.View() + "\n\n")
	b.WriteString(mutedSty.Render("Enter backup · Esc cancel"))
	return m.dialogBox(b.String())
}

func (m *Model) importView() string {
	var b strings.Builder
	b.WriteString(brandSty.Render("Import from backup") + "\n\n")
	b.WriteString(m.input.View() + "\n\n")
	b.WriteString(mutedSty.Render("Notes and images are restored. Existing notes skipped.\nEnter import · Esc cancel"))
	return m.dialogBox(b.String())
}

func (m Model) dialogBox(content string) string {
	dialog := lipgloss.NewStyle().
		Background(surface).
		Foreground(text).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(muted).
		Padding(1, 3).
		Width(min(80, max(40, m.width-6))).
		Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog, lipgloss.WithWhitespaceBackground(bg))
}

func formatNum(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	s := fmt.Sprintf("%d", n)
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, ",")
}
