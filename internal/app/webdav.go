package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WebDAVConfig holds sync settings.
type WebDAVConfig struct {
	URL            string `json:"url"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	RemotePath     string `json:"remote_path"`
	SyncEnabled    bool   `json:"sync_enabled"`
	AutoSyncMins   int    `json:"auto_sync_minutes"`
}

// webdavConfigPath returns the path to the WebDAV config file.
func webdavConfigPath(notebookRoot string) string {
	return filepath.Join(notebookRoot, ".webdav.json")
}

// loadWebDAVConfig reads config from disk.
func loadWebDAVConfig(notebookRoot string) WebDAVConfig {
	config := WebDAVConfig{RemotePath: "/tn"}
	data, err := os.ReadFile(webdavConfigPath(notebookRoot))
	if err != nil {
		return config
	}
	_ = json.Unmarshal(data, &config)
	return config
}

// saveWebDAVConfig writes config to disk.
func saveWebDAVConfig(notebookRoot string, config WebDAVConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(webdavConfigPath(notebookRoot), data, 0o600)
}

// webdavSyncStats holds results of a sync operation.
type webdavSyncStats struct {
	Uploaded   int
	Downloaded int
	Deleted    int
	Conflicts  int
	Errors     int
}

// doWebDAVSync performs bidirectional sync.
// For now this is a placeholder that reports "not configured".
// A full implementation would use PROPFIND, PUT, GET, DELETE.
func (m *Model) doWebDAVSync() webdavSyncStats {
	config := loadWebDAVConfig(m.store.Root)
	if !config.SyncEnabled || config.URL == "" {
		m.flashStatus("WebDAV not configured · Ctrl+Shift+S to set up", true, 3*time.Second)
		return webdavSyncStats{}
	}
	// Placeholder: full WebDAV implementation would:
	// 1. PROPFind remote tree
	// 2. Compare with local tree
	// 3. PUT new/modified local files
	// 4. GET new/modified remote files
	// 5. Handle conflicts
	m.flashStatus("Sync not yet implemented", true, 3*time.Second)
	return webdavSyncStats{}
}

// fetchWebDAV performs a WebDAV PROPFIND request.
func fetchWebDAV(config WebDAVConfig, path string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("PROPFIND", config.URL+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(config.Username, config.Password)
	req.Header.Set("Depth", "1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// uploadWebDAV performs a WebDAV PUT request.
func uploadWebDAV(config WebDAVConfig, path string, data []byte) error {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("PUT", config.URL+path, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.SetBasicAuth(config.Username, config.Password)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("PUT %s: %s", path, resp.Status)
	}
	return nil
}
