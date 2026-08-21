package app

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vst93/tn/internal/storage"
)

// WebDAVConfig holds sync settings.
type WebDAVConfig struct {
	URL          string `json:"url"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	RemotePath   string `json:"remote_path"`
	SyncEnabled  bool   `json:"sync_enabled"`
	AutoSyncMins int    `json:"auto_sync_minutes"`
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

// webdavRemoteFile represents a file on the remote WebDAV server.
type webdavRemoteFile struct {
	Path         string
	LastModified time.Time
	Size         int64
	ETag         string
}

// webdavPROPFINDResponse parses WebDAV XML responses.
type webdavPROPFINDResponse struct {
	XMLName   xml.Name         `xml:"multistatus"`
	Responses []webdavResponse `xml:"response"`
}

type webdavResponse struct {
	Href     string         `xml:"href"`
	PropStat webdavPropStat `xml:"propstat"`
}

type webdavPropStat struct {
	Prop   webdavProp `xml:"prop"`
	Status string     `xml:"status"`
}

type webdavProp struct {
	LastModified  string `xml:"getlastmodified"`
	ContentLength string `xml:"getcontentlength"`
	ETag          string `xml:"getetag"`
}

// httpTimeout for WebDAV operations.
const httpTimeout = 30 * time.Second

// webdavClient wraps http.Client with auth.
type webdavClient struct {
	config WebDAVConfig
	client *http.Client
}

// newWebDAVClient creates an authenticated WebDAV client.
func newWebDAVClient(config WebDAVConfig) *webdavClient {
	return &webdavClient{
		config: config,
		client: &http.Client{Timeout: httpTimeout},
	}
}

// propfind performs a PROPFIND request.
func (c *webdavClient) propfind(path string) ([]byte, error) {
	req, err := http.NewRequest("PROPFIND", c.config.URL+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Depth", "1")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("PROPFIND %s: %s", path, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// put performs a PUT request.
func (c *webdavClient) put(path string, data []byte) error {
	req, err := http.NewRequest("PUT", c.config.URL+path, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.config.Username, c.config.Password)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("PUT %s: %s", path, resp.Status)
	}
	return nil
}

// get performs a GET request.
func (c *webdavClient) get(path string) ([]byte, error) {
	req, err := http.NewRequest("GET", c.config.URL+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.config.Username, c.config.Password)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// delete performs a DELETE request.
func (c *webdavClient) delete(path string) error {
	req, err := http.NewRequest("DELETE", c.config.URL+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.config.Username, c.config.Password)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		return fmt.Errorf("DELETE %s: %s", path, resp.Status)
	}
	return nil
}

// mkcol performs a MKCOL request.
func (c *webdavClient) mkcol(path string) error {
	req, err := http.NewRequest("MKCOL", c.config.URL+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.config.Username, c.config.Password)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 405 {
		return fmt.Errorf("MKCOL %s: %s", path, resp.Status)
	}
	return nil
}

// listRemote lists files at the given remote path.
func (c *webdavClient) listRemote(path string) ([]webdavRemoteFile, error) {
	data, err := c.propfind(path)
	if err != nil {
		return nil, err
	}
	return parsePROPFIND(data)
}

// parsePROPFIND parses a WebDAV PROPFIND XML response.
func parsePROPFIND(data []byte) ([]webdavRemoteFile, error) {
	var result webdavPROPFINDResponse
	if err := xml.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	var files []webdavRemoteFile
	for _, resp := range result.Responses {
		if resp.PropStat.Status != "" && !strings.Contains(resp.PropStat.Status, "200") {
			continue
		}
		f := webdavRemoteFile{Path: resp.Href}
		if resp.PropStat.Prop.LastModified != "" {
			t, err := time.Parse(time.RFC1123, resp.PropStat.Prop.LastModified)
			if err == nil {
				f.LastModified = t
			}
		}
		fmt.Sscanf(resp.PropStat.Prop.ContentLength, "%d", &f.Size)
		f.ETag = resp.PropStat.Prop.ETag
		files = append(files, f)
	}
	return files, nil
}

// syncResult contains the outcome of a sync operation.
type syncResult struct {
	Uploaded   int
	Downloaded int
	Deleted    int
	Conflicts  int
	Errors     int
	LastErr    error
}

// doWebDAVSync performs bidirectional sync between local and remote.
func (m *Model) doWebDAVSync() syncResult {
	config := loadWebDAVConfig(m.store.Root)
	if !config.SyncEnabled || config.URL == "" {
		return syncResult{}
	}
	client := newWebDAVClient(config)
	remotePath := config.RemotePath
	if remotePath == "" {
		remotePath = "/tn"
	}

	// Ensure remote root exists.
	_ = client.mkcol(remotePath)

	result := syncResult{}

	// Collect local files (notes + images).
	localNotes := m.collectLocalNotes()
	localImages := m.collectLocalImages()

	// Collect remote files.
	remoteFiles, err := client.listRemote(remotePath)
	if err != nil {
		result.Errors++
		result.LastErr = err
		m.flashStatus("Sync failed: "+err.Error(), true, 3*time.Second)
		return result
	}

	// Build remote path map.
	remoteMap := make(map[string]webdavRemoteFile)
	for _, rf := range remoteFiles {
		// Strip the remote prefix from the href.
		rel := strings.TrimPrefix(rf.Path, remotePath)
		rel = strings.TrimPrefix(rel, "/")
		if rel != "" {
			remoteMap[rel] = rf
		}
	}

	// Upload notes.
	for relPath, content := range localNotes {
		_, exists := remoteMap[relPath]
		if !exists || true { // Always upload for now.
			remoteFilePath := remotePath + "/" + relPath
			parentDir := filepath.Dir(relPath)
			if parentDir != "." && parentDir != "/" {
				_ = client.mkcol(remotePath + "/" + parentDir)
			}
			if err := client.put(remoteFilePath, []byte(content)); err != nil {
				result.Errors++
				result.LastErr = err
			} else {
				result.Uploaded++
			}
		}
	}

	// Upload images.
	for relPath, absPath := range localImages {
		data, err := os.ReadFile(absPath)
		if err != nil {
			result.Errors++
			continue
		}
		rf, exists := remoteMap[relPath]
		if !exists || true {
			remoteFilePath := remotePath + "/" + relPath
			_ = client.mkcol(remotePath + "/images")
			if err := client.put(remoteFilePath, data); err != nil {
				result.Errors++
				result.LastErr = err
			} else {
				result.Uploaded++
			}
		}
		_ = rf
	}

	// Download remote files not present locally.
	for _, rf := range remoteFiles {
		rel := strings.TrimPrefix(rf.Path, remotePath)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			continue
		}
		if _, exists := localNotes[rel]; !exists {
			if _, exists := localImages[rel]; !exists {
				data, err := client.get(rf.Path)
				if err != nil {
					result.Errors++
					continue
				}
				localPath := filepath.Join(m.store.Root, rel)
				if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err == nil {
					if err := os.WriteFile(localPath, data, 0o644); err == nil {
						result.Downloaded++
					} else {
						result.Errors++
					}
				}
			}
		}
	}

	return result
}

// collectLocalNotes returns all note contents keyed by relative path.
func (m *Model) collectLocalNotes() map[string]string {
	notes := make(map[string]string)
	var walk func(nodes []*storage.Node)
	walk = func(nodes []*storage.Node) {
		for _, n := range nodes {
			if n.IsDir {
				walk(n.Children)
				continue
			}
			if content, err := m.store.Read(n.RelPath); err == nil {
				notes[n.RelPath] = content
			}
		}
	}
	walk(m.tree)
	return notes
}

// collectLocalImages returns all local image files keyed by relative path.
func (m *Model) collectLocalImages() map[string]string {
	images := make(map[string]string)
	dir := filepath.Join(m.store.Root, "images")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return images
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		relPath := "images/" + entry.Name()
		images[relPath] = filepath.Join(dir, entry.Name())
	}
	return images
}
