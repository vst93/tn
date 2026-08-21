package app

// Minimal UI language support: tr() picks the string for the active language.
// English is the default; the choice is stored in the session file and can be
// toggled from the command palette ("Language / 语言").

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type uiLang string

const (
	LangEN uiLang = "en"
	LangZH uiLang = "zh"
)

var lang uiLang = LangEN

// tr returns the Chinese string when the UI language is Chinese, otherwise
// the English one (which doubles as the in-code default).
func tr(en, zh string) string {
	if lang == LangZH {
		return zh
	}
	return en
}

// uiPrefsPath stores language (and future UI preferences) next to the session.
func uiPrefsPath(sessionPath string) string {
	dir := filepath.Dir(sessionPath)
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "ui.json")
}

type uiPrefs struct {
	Lang uiLang `json:"lang"`
}

func loadUiPrefs(sessionPath string) {
	data, err := os.ReadFile(uiPrefsPath(sessionPath))
	if err != nil {
		return
	}
	var p uiPrefs
	if json.Unmarshal(data, &p) == nil && (p.Lang == LangEN || p.Lang == LangZH) {
		lang = p.Lang
	}
}

func saveUiPrefs(sessionPath string) {
	p := uiPrefs{Lang: lang}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return
	}
	path := uiPrefsPath(sessionPath)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, data, 0o600)
}

func toggleLang() {
	if lang == LangEN {
		lang = LangZH
	} else {
		lang = LangEN
	}
}

// helpGroupTitleZh maps a help group title to its Chinese counterpart.
func helpGroupTitleZh(title string) string {
	switch title {
	case "Navigate":
		return "导航"
	case "Notes":
		return "笔记"
	case "Copy":
		return "复制"
	case "Search":
		return "搜索"
	case "Data":
		return "数据"
	case "Git 同步", "Git sync":
		return "Git 同步"
	case "App":
		return "应用"
	case "Edit Markdown":
		return "Markdown 编辑"
	}
	return title
}

// helpRowDescZh maps a help row description to Chinese. Rows not listed keep
// the English text.
func helpRowDescZh(r helpRow) string {
	m := map[string]string{
		// Navigate
		"Select item":            "选择条目",
		"Collapse / expand":      "折叠 / 展开",
		"Open note":              "打开笔记",
		"Quick open note":        "快速打开",
		"Switch panel":           "切换面板",
		"Toggle tree visibility": "显示/隐藏列表",
		"Back / forward history": "后退 / 前进",
		"Page preview down / up": "预览下/上翻页",
		"Find in note":           "笔记内查找",
		// Notes
		"New note":         "新建笔记",
		"New folder":       "新建文件夹",
		"Rename":           "重命名",
		"Delete":           "删除",
		"Pin / unpin note": "置顶 / 取消置顶",
		"Edit / preview":   "编辑 / 预览",
		"Save":             "保存",
		"Save (preview)":   "保存（预览）",
		"Undo":             "撤销",
		"Redo":             "重做",
		"Edit tags":        "编辑标签",
		"Filter by tag":    "按标签过滤",
		"Command palette":  "命令面板",
		// Copy
		"Copy note (preview)":          "复制笔记（预览）",
		"Copy selection (edit) / quit": "复制选中文本（编辑）/ 退出",
		"Cut selection (edit)":         "剪切选中内容（编辑）",
		"Paste":                        "粘贴",
		"Select all (edit)":            "全选（编辑）",
		"Select text (edit)":           "选中文本（编辑）",
		"Select text (edit/preview)":   "选中文本（编辑/预览）",
		"Copy current line (edit)":     "复制当前行（编辑）",
		"Select terminal text":         "终端文本选择",
		// Search
		"Search note":       "笔记内搜索",
		"Search everywhere": "全局搜索",
		"Go to line":        "跳转到行",
		// Data
		"Export note":        "导出笔记",
		"Export as HTML":     "导出 HTML",
		"Backup notes":       "备份笔记",
		"Import from backup": "从备份导入",
		// Git
		"同步设置（远程/分支/自动）": "同步设置（远程/分支/自动）",
		"推送":             "推送",
		"拉取":             "拉取",
		"笔记历史版本 / 回退":    "笔记历史版本 / 回退",
		// App
		"Focus mode":                             "专注模式",
		"Switch theme":                           "切换主题",
		"Refresh":                                "刷新",
		"Quit (dirty: Ctrl+C twice force-quits)": "退出（有未保存更改时按两次 Ctrl+C 强制退出）",
		"Close / cancel":                         "关闭 / 取消",
		// Edit Markdown
		"Slash menu":       "斜杠菜单",
		"Bold":             "加粗",
		"Italic":           "斜体",
		"Insert link":      "插入链接",
		"Copy line (edit)": "复制行（编辑）",
		"Paste image":      "粘贴图片",
	}
	if zh, ok := m[r.desc]; ok {
		return zh
	}
	return r.desc
}

// langFlash reports the language after a toggle.
func langFlash() string {
	if lang == LangZH {
		return "界面语言：中文"
	}
	return "Language: English"
}
