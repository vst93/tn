package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vst93/tn/internal/app"
	"github.com/vst93/tn/internal/storage"
)

var version = "dev"

func main() {
	rootFlag := flag.String("dir", "", "notebook directory (default: ~/.tn)")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()

	if *showVersion {
		fmt.Println("tn", version)
		return
	}

	root := *rootFlag
	if root == "" {
		var err error
		root, err = storage.DefaultRoot()
		if err != nil {
			fatal(err)
		}
	}
	store := storage.New(root)
	if err := store.Init(); err != nil {
		fatal(err)
	}

	program := tea.NewProgram(app.New(store), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "tn:", err)
	os.Exit(1)
}
