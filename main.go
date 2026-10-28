package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

// ============================================================
// Types
// ============================================================

// Mode represents which operation mode we're in.
type Mode int

const (
	ModePortrait   Mode = iota // 原画拷贝
	ModeDeath                  // 阵亡语音拷贝
	ModeSkill                  // 技能语音拷贝
	ModeDownload               // 语音下载
	ModeSkillFile              // 技能文件拷贝
	ModeSkillTest              // 技能测试
	ModeQuickStart             // 快速启动配置
)

var modeLabels = []string{"原画拷贝", "阵亡语音拷贝", "技能语音拷贝", "语音下载", "技能文件拷贝", "测试技能", "快速启动配置"}

// Stage represents the current screen.
type Stage int

const (
	StageModeSelect Stage = iota
	StageForm
	StageConfirm
	StageDownload
	StageTestRunning
	StageTestOutput
	StageDone
)

// ============================================================
// Styles
// ============================================================

var (
	styleTitle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).MarginBottom(1)
	styleModeActive   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Padding(0, 1)
	styleModeInactive = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Padding(0, 1)
	styleLabel        = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	styleHelp         = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	styleSuccess      = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	styleError        = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	styleDropdown     = lipgloss.NewStyle().Background(lipgloss.Color("236")).Padding(0, 1)
	styleDropdownSel  = lipgloss.NewStyle().Background(lipgloss.Color("57")).Foreground(lipgloss.Color("15")).Padding(0, 1)
	styleDim          = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	stylePrompt       = lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
)

// ============================================================
// File scanning utilities
// ============================================================

const packagesDir = "packages"

// listPackages returns all package directory names under packages/.
func listPackages(base string) []string {
	dir := filepath.Join(base, packagesDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var pkgs []string
	for _, e := range entries {
		if e.IsDir() {
			pkgs = append(pkgs, e.Name())
		}
	}
	sort.Strings(pkgs)
	return pkgs
}

// listGenerals returns unique general names from a package's image/generals/
// and audio/death/ directories.
// The general name is the full filename stem, e.g. "chunyu1__huangwudie".
func listGenerals(base, pkg string) []string {
	seen := map[string]bool{}

	for _, sub := range []string{"image/generals", "audio/death"} {
		dir := filepath.Join(base, packagesDir, pkg, sub)
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
				seen[name] = true
			}
		}
	}

	var result []string
	for k := range seen {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// listSkills returns unique skill names from a package's audio/skill/.
// Skill name is the file stem without trailing digits, e.g. "chunyu__aoxi" from "chunyu__aoxi1.mp3".
var skillRe = regexp.MustCompile(`^(.+?)(\d+)\.mp3$`)

func listSkills(base, pkg string) []string {
	skillDir := filepath.Join(base, packagesDir, pkg, "audio", "skill")
	entries, err := os.ReadDir(skillDir)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := skillRe.FindStringSubmatch(e.Name())
		if len(m) == 3 {
			seen[m[1]] = true
		}
	}
	var result []string
	for k := range seen {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// listGeneralsAll returns generals from all packages (for initial unfiltered state).
func listGeneralsAll(base string) []string {
	seen := map[string]bool{}
	pkgs := listPackages(base)
	for _, pkg := range pkgs {
		for _, g := range listGenerals(base, pkg) {
			seen[g] = true
		}
	}
	var result []string
	for k := range seen {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// listSkillsAll returns skills from all packages.
func listSkillsAll(base string) []string {
	seen := map[string]bool{}
	pkgs := listPackages(base)
	for _, pkg := range pkgs {
		for _, s := range listSkills(base, pkg) {
			seen[s] = true
		}
	}
	var result []string
	for k := range seen {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// ============================================================
// AutoField - text input with tab completion
// ============================================================

// AutoField wraps textinput.Model with fzf-style completion.
type AutoField struct {
	input        textinput.Model
	label        string
	candidates   []string // all possible completions
	filtered     []string // currently filtered matches
	showDropdown bool
	cursor       int // cursor in dropdown
}

func newAutoField(label, placeholder string) AutoField {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.CharLimit = 200
	ti.Width = 50
	return AutoField{
		input:  ti,
		label:  label,
		cursor: 0,
	}
}

// SetCandidates sets the full candidate list.
func (f *AutoField) SetCandidates(c []string) {
	f.candidates = c
}

// FuzzyFilter recomputes filtered list based on current input value.
func (f *AutoField) FuzzyFilter() {
	val := f.input.Value()
	if val == "" {
		f.filtered = f.candidates
	} else {
		matches := fuzzy.Find(val, f.candidates)
		f.filtered = make([]string, len(matches))
		for i, m := range matches {
			f.filtered[i] = m.Str
		}
	}
	if len(f.filtered) == 0 {
		f.showDropdown = false
		return
	}
	if f.cursor >= len(f.filtered) {
		f.cursor = len(f.filtered) - 1
	}
	if f.cursor < 0 {
		f.cursor = 0
	}
}

// AcceptSelection accepts the highlighted completion.
func (f *AutoField) AcceptSelection() {
	if f.showDropdown && len(f.filtered) > 0 && f.cursor < len(f.filtered) {
		f.input.SetValue(f.filtered[f.cursor])
		f.input.CursorEnd()
		f.showDropdown = false
		f.cursor = 0
		f.FuzzyFilter() // refresh to update
	}
}

// Blur hides the dropdown.
func (f *AutoField) Blur() {
	f.showDropdown = false
	f.input.Blur()
}

// Focus focuses the input.
func (f *AutoField) Focus() {
	f.showDropdown = false
	f.cursor = 0
	f.input.Focus()
}

// ============================================================
// Main Model
// ============================================================

type model struct {
	basePath    string
	stage       Stage
	mode        Mode
	activeField int
	fields      []*AutoField
	status      string
	statusOK    bool
	width       int
	height      int

	// quickStartLaunch 为 true 时,快速启动配置写入成功后退出 TUI 并启动 FreeKill
	quickStartLaunch bool

	// 技能测试运行中界面所需状态
	testFrame     int
	testSkillName string
	testFilePath  string

	// 技能测试输出查看页状态
	testOutput       string
	testOutputOK     bool
	testOutputScroll int

	// modeCache stores field values per mode so switching preserves content.
	// Only fields 2 and 3 (general/skill) are cached per-mode.
	// Fields 0 and 1 (packages) are shared across all modes.
	modeCache map[Mode][2]string

	// Cached all-generals and all-skills lists to avoid repeated filesystem scans.
	cachedGeneralsAll []string
	cachedSkillsAll   []string
	cachedSkillIDsAll []string
}

func newModel() model {
	return model{
		basePath:  ".",
		stage:     StageModeSelect,
		modeCache: make(map[Mode][2]string),
	}
}

// setFieldsForMode configures the input fields based on the selected mode.
// Saves current field values to per-mode cache, restores from cache for the new mode.
// Fields 0 and 1 (packages) are shared across all modes and always preserved.
func (m *model) setFieldsForMode() {
	if m.mode == ModeDownload {
		m.setupDownloadFields()
		return
	}
	if m.mode == ModeSkillFile {
		m.setupSkillFileFields()
		return
	}
	if m.mode == ModeSkillTest {
		m.setupSkillTestFields()
		return
	}
	if m.mode == ModeQuickStart {
		m.setupQuickStartFields()
		return
	}

	// Save current per-mode fields (2 & 3) to cache
	m.saveToCache()

	// Read current package values
	var pkgSrc, pkgDst string
	if m.fields != nil && len(m.fields) >= 2 {
		pkgSrc = m.fields[0].input.Value()
		pkgDst = m.fields[1].input.Value()
	}

	// Read cached values for the target mode
	cached, hasCache := m.modeCache[m.mode]
	var gen1, gen2 string
	if hasCache {
		gen1 = cached[0]
		gen2 = cached[1]
	}

	switch m.mode {
	case ModePortrait:
		f1 := newAutoField("来源包名", "输入包名，Tab 补全")
		f1.SetCandidates(listPackages(m.basePath))
		f1.input.SetValue(pkgSrc)

		f2 := newAutoField("目标包名", "输入包名，Tab 补全")
		f2.SetCandidates(listPackages(m.basePath))
		f2.input.SetValue(pkgDst)

		f3 := newAutoField("来源武将", "输入武将名，Tab 补全")
		f3.SetCandidates(m.generalsAll())
		f3.input.SetValue(gen1)

		f4 := newAutoField("目标武将", "输入武将名，Tab 补全")
		f4.SetCandidates(m.generalsAll())
		f4.input.SetValue(gen2)

		m.fields = []*AutoField{&f1, &f2, &f3, &f4}

	case ModeDeath:
		f1 := newAutoField("来源包名", "输入包名，Tab 补全")
		f1.SetCandidates(listPackages(m.basePath))
		f1.input.SetValue(pkgSrc)

		f2 := newAutoField("目标包名", "输入包名，Tab 补全")
		f2.SetCandidates(listPackages(m.basePath))
		f2.input.SetValue(pkgDst)

		f3 := newAutoField("来源武将", "输入武将名，Tab 补全")
		f3.SetCandidates(m.generalsAll())
		f3.input.SetValue(gen1)

		f4 := newAutoField("目标武将", "输入武将名，Tab 补全")
		f4.SetCandidates(m.generalsAll())
		f4.input.SetValue(gen2)

		m.fields = []*AutoField{&f1, &f2, &f3, &f4}

	case ModeSkill:
		f1 := newAutoField("来源包名", "输入包名，Tab 补全")
		f1.SetCandidates(listPackages(m.basePath))
		f1.input.SetValue(pkgSrc)

		f2 := newAutoField("目标包名", "输入包名，Tab 补全")
		f2.SetCandidates(listPackages(m.basePath))
		f2.input.SetValue(pkgDst)

		f3 := newAutoField("来源技能", "输入技能名，Tab 补全")
		f3.SetCandidates(m.skillsAll())
		f3.input.SetValue(gen1)

		f4 := newAutoField("目标技能", "输入新技能名")
		f4.SetCandidates(m.skillsAll())
		f4.input.SetValue(gen2)

		m.fields = []*AutoField{&f1, &f2, &f3, &f4}
	}

	m.activeField = 0
	if len(m.fields) > 0 {
		m.fields[0].Focus()
	}
	m.stage = StageForm
}

// setupDownloadFields configures the download form.
func (m *model) setupDownloadFields() {
	oldPkg, oldType, oldID, oldNum, oldURL := "", "", "", "", ""
	if m.fields != nil {
		if len(m.fields) >= 1 {
			oldPkg = m.fields[0].input.Value()
		}
		if len(m.fields) >= 2 {
			oldType = m.fields[1].input.Value()
		}
		if len(m.fields) >= 3 {
			oldID = m.fields[2].input.Value()
		}
		if len(m.fields) >= 4 {
			oldNum = m.fields[3].input.Value()
		}
		if len(m.fields) >= 5 {
			oldURL = m.fields[4].input.Value()
		}
	}

	f1 := newAutoField("目标包名", "输入包名，Tab 补全")
	f1.SetCandidates(listPackages(m.basePath))
	f1.input.SetValue(oldPkg)

	f2 := newAutoField("语音类型", "技能语音 / 阵亡语音 (Tab 切换)")
	if oldType == "" {
		f2.input.SetValue("技能语音")
	} else {
		f2.input.SetValue(oldType)
	}
	f2.input.CharLimit = 10
	f2.input.Width = 10

	f3 := newAutoField("语音标识", "如 wycf_kangge 或 wycf__khan")
	f3.input.SetValue(oldID)

	f4 := newAutoField("编号(技能)", "输入编号，如 1；留空=无编号")
	f4.input.SetValue(oldNum)
	f4.input.CharLimit = 4
	f4.input.Width = 4

	f5 := newAutoField("下载链接", "粘贴 mp3 链接")
	f5.input.SetValue(oldURL)

	m.fields = []*AutoField{&f1, &f2, &f3, &f4, &f5}

	m.activeField = 0
	if len(m.fields) > 0 {
		m.fields[0].Focus()
	}
	m.stage = StageDownload
}

// setupSkillFileFields configures the skill file copy form (6 fields).
func (m *model) setupSkillFileFields() {
	oldSrcSub, oldSrcID, oldDstSub, oldDstID := "", "", "", ""
	var pkgSrc, pkgDst string
	if m.fields != nil {
		if len(m.fields) >= 2 {
			pkgSrc = m.fields[0].input.Value()
			pkgDst = m.fields[1].input.Value()
		}
		if len(m.fields) >= 6 {
			oldSrcSub = m.fields[2].input.Value()
			oldSrcID = m.fields[3].input.Value()
			oldDstSub = m.fields[4].input.Value()
			oldDstID = m.fields[5].input.Value()
		}
	}

	f1 := newAutoField("来源包名", "输入包名，Tab 补全")
	f1.SetCandidates(listPackages(m.basePath))
	f1.input.SetValue(pkgSrc)

	f2 := newAutoField("目标包名", "输入包名，Tab 补全")
	f2.SetCandidates(listPackages(m.basePath))
	f2.input.SetValue(pkgDst)

	f3 := newAutoField("来源小包(可选)", "留空扫描整个包")
	if pkgSrc != "" {
		f3.SetCandidates(listSubPackages(m.basePath, pkgSrc))
	} else {
		f3.SetCandidates(listSubPackagesAll(m.basePath))
	}
	f3.input.SetValue(oldSrcSub)

	f4 := newAutoField("来源技能ID", "如 chunyu__langqing，Tab 补全")
	if pkgSrc != "" {
		f4.SetCandidates(listSkillIDs(m.basePath, pkgSrc, oldSrcSub))
	} else {
		f4.SetCandidates(m.skillIDsAll())
	}
	f4.input.SetValue(oldSrcID)

	f5 := newAutoField("目标小包", "输入的技能文件写入哪个小包")
	if pkgDst != "" {
		f5.SetCandidates(listSubPackages(m.basePath, pkgDst))
	} else {
		f5.SetCandidates(listSubPackagesAll(m.basePath))
	}
	f5.input.SetValue(oldDstSub)

	f6 := newAutoField("目标技能ID", "wanyi__newskill")
	f6.input.SetValue(oldDstID)

	m.fields = []*AutoField{&f1, &f2, &f3, &f4, &f5, &f6}

	m.activeField = 0
	if len(m.fields) > 0 {
		m.fields[0].Focus()
	}
	m.stage = StageForm
}

// setupSkillTestFields configures the skill test form (3 fields).
func (m *model) setupSkillTestFields() {
	oldPkg, oldSub, oldID := "", "", ""
	if m.fields != nil {
		if len(m.fields) >= 1 {
			oldPkg = m.fields[0].input.Value()
		}
		if len(m.fields) >= 2 {
			oldSub = m.fields[1].input.Value()
		}
		if len(m.fields) >= 3 {
			oldID = m.fields[2].input.Value()
		}
	}

	f1 := newAutoField("目标包名", "输入包名，Tab 补全")
	f1.SetCandidates(listPackages(m.basePath))
	f1.input.SetValue(oldPkg)

	f2 := newAutoField("小包(可选)", "留空扫描整个包")
	if oldPkg != "" {
		f2.SetCandidates(listSubPackages(m.basePath, oldPkg))
	} else {
		f2.SetCandidates(listSubPackagesAll(m.basePath))
	}
	f2.input.SetValue(oldSub)

	f3 := newAutoField("技能ID", "如 wycf_jianhua，Tab 补全")
	if oldPkg != "" {
		f3.SetCandidates(listSkillIDs(m.basePath, oldPkg, oldSub))
	} else {
		f3.SetCandidates(m.skillIDsAll())
	}
	f3.input.SetValue(oldID)

	m.fields = []*AutoField{&f1, &f2, &f3}

	m.activeField = 0
	if len(m.fields) > 0 {
		m.fields[0].Focus()
	}
	m.stage = StageForm
}

// setupQuickStartFields configures the quick-start config form.
func (m *model) setupQuickStartFields() {
	oldMode, oldNum, oldGen, oldRole, oldProp, oldControl := "", "", "", "", "", ""
	if m.fields != nil {
		if len(m.fields) >= 1 {
			oldMode = m.fields[0].input.Value()
		}
		if len(m.fields) >= 2 {
			oldNum = m.fields[1].input.Value()
		}
		if len(m.fields) >= 3 {
			oldGen = m.fields[2].input.Value()
		}
		if len(m.fields) >= 4 {
			oldRole = m.fields[3].input.Value()
		}
		if len(m.fields) >= 5 {
			oldProp = m.fields[4].input.Value()
		}
		if len(m.fields) >= 6 {
			oldControl = m.fields[5].input.Value()
		}
	}

	f1 := newAutoField("游戏模式", "默认 aaa_role_mode")
	if oldMode == "" {
		f1.input.SetValue("aaa_role_mode")
	} else {
		f1.input.SetValue(oldMode)
	}

	f2 := newAutoField("玩家人数", "默认 2")
	if oldNum == "" {
		f2.input.SetValue("2")
	} else {
		f2.input.SetValue(oldNum)
	}
	f2.input.CharLimit = 3
	f2.input.Width = 4

	f3 := newAutoField("武将配置", "如 1:caocao, 2:guanyu;留空自动补谋徐盛+士兵")
	f3.input.SetValue(oldGen)

	f4 := newAutoField("身份配置", "如 1:lord, 2:rebel;留空自动补默认身份(按人数标准身份表)")
	f4.input.SetValue(oldRole)

	f5 := newAutoField("属性配置", "如 1:hp=4; 1:cards=slash,crossbow(多个用;分隔,值内逗号=数组)")
	f5.input.SetValue(oldProp)

	f6 := newAutoField("全控开关", "true/false,默认 true")
	if oldControl == "" {
		f6.input.SetValue("true")
	} else {
		f6.input.SetValue(oldControl)
	}
	f6.input.CharLimit = 6
	f6.input.Width = 6

	m.fields = []*AutoField{&f1, &f2, &f3, &f4, &f5, &f6}

	m.activeField = 0
	if len(m.fields) > 0 {
		m.fields[0].Focus()
	}
	m.stage = StageForm
}

func (m *model) saveToCache() {
	if m.fields == nil {
		return
	}
	if m.mode == ModeSkillFile {
		// SkillFile fields are preserved via setupSkillFileFields re-reading, no cache needed
		return
	}
	if len(m.fields) >= 4 {
		var cached [2]string
		cached[0] = m.fields[2].input.Value()
		cached[1] = m.fields[3].input.Value()
		m.modeCache[m.mode] = cached
	}
}

// values returns the string values of all fields.
func (m *model) values() []string {
	vals := make([]string, len(m.fields))
	for i, f := range m.fields {
		vals[i] = f.input.Value()
	}
	return vals
}

// allFilled checks if all fields are non-empty.
func (m *model) allFilled() bool {
	for _, f := range m.fields {
		if strings.TrimSpace(f.input.Value()) == "" {
			return false
		}
	}
	return true
}

// formCanSubmit checks whether the form can be submitted.
// For SkillFile mode, field 2 (来源小包) is optional.
// For SkillTest mode, field 1 (小包) is optional.
// For QuickStart mode, all fields are optional (身份/属性留空=随机/不设置,其余有默认值).
func (m *model) formCanSubmit() bool {
	if m.mode == ModeSkillFile {
		for i, f := range m.fields {
			if i == 2 {
				continue // 来源小包 is optional
			}
			if strings.TrimSpace(f.input.Value()) == "" {
				return false
			}
		}
		return true
	}
	if m.mode == ModeSkillTest {
		for i, f := range m.fields {
			if i == 1 {
				continue // 小包 is optional
			}
			if strings.TrimSpace(f.input.Value()) == "" {
				return false
			}
		}
		return true
	}
	if m.mode == ModeQuickStart {
		return true // 快速启动配置所有字段均可选,留空使用默认行为
	}
	return m.allFilled()
}

// generalsAll returns cached list of all generals.
func (m *model) generalsAll() []string {
	if m.cachedGeneralsAll == nil {
		m.cachedGeneralsAll = listGeneralsAll(m.basePath)
	}
	return m.cachedGeneralsAll
}

// skillsAll returns cached list of all skills.
func (m *model) skillsAll() []string {
	if m.cachedSkillsAll == nil {
		m.cachedSkillsAll = listSkillsAll(m.basePath)
	}
	return m.cachedSkillsAll
}

// cachedSkillIDsAll returns cached list of all skill IDs.
func (m *model) skillIDsAll() []string {
	if m.cachedSkillIDsAll == nil {
		m.cachedSkillIDsAll = listSkillIDsAll(m.basePath)
	}
	return m.cachedSkillIDsAll
}

// refreshCompletions updates candidates for dependent fields (generals/skills)
// based on the currently entered package name.
func (m *model) refreshCompletions(fieldIdx int) {
	switch m.mode {
	case ModePortrait, ModeDeath:
		srcPkg := m.fields[0].input.Value()
		dstPkg := m.fields[1].input.Value()
		if fieldIdx <= 1 {
			if srcPkg != "" {
				m.fields[2].SetCandidates(listGenerals(m.basePath, srcPkg))
			} else {
				m.fields[2].SetCandidates(m.generalsAll())
			}
			if dstPkg != "" {
				m.fields[3].SetCandidates(listGenerals(m.basePath, dstPkg))
			} else {
				m.fields[3].SetCandidates(m.generalsAll())
			}
		} else {
			if fieldIdx == 2 && srcPkg != "" {
				m.fields[2].SetCandidates(listGenerals(m.basePath, srcPkg))
			} else if fieldIdx == 3 && dstPkg != "" {
				m.fields[3].SetCandidates(listGenerals(m.basePath, dstPkg))
			}
		}
	case ModeSkill:
		srcPkg := m.fields[0].input.Value()
		dstPkg := m.fields[1].input.Value()
		if fieldIdx <= 1 {
			if srcPkg != "" {
				m.fields[2].SetCandidates(listSkills(m.basePath, srcPkg))
			} else {
				m.fields[2].SetCandidates(m.skillsAll())
			}
			if dstPkg != "" {
				m.fields[3].SetCandidates(listSkills(m.basePath, dstPkg))
			} else {
				m.fields[3].SetCandidates(m.skillsAll())
			}
		} else {
			if fieldIdx == 2 && srcPkg != "" {
				m.fields[2].SetCandidates(listSkills(m.basePath, srcPkg))
			} else if fieldIdx == 3 && dstPkg != "" {
				m.fields[3].SetCandidates(listSkills(m.basePath, dstPkg))
			}
		}

	case ModeSkillFile:
		srcPkg := m.fields[0].input.Value()
		dstPkg := m.fields[1].input.Value()
		srcSub := m.fields[2].input.Value()

		switch fieldIdx {
		case 0:
			// Source package changed → update source sub-pkg & skill ID candidates
			if srcPkg != "" {
				m.fields[2].SetCandidates(listSubPackages(m.basePath, srcPkg))
				m.fields[3].SetCandidates(listSkillIDs(m.basePath, srcPkg, srcSub))
			} else {
				m.fields[2].SetCandidates(listSubPackagesAll(m.basePath))
				m.fields[3].SetCandidates(m.skillIDsAll())
			}
		case 1:
			// Dest package changed → update dest sub-pkg candidates
			if dstPkg != "" {
				m.fields[4].SetCandidates(listSubPackages(m.basePath, dstPkg))
			} else {
				m.fields[4].SetCandidates(listSubPackagesAll(m.basePath))
			}
		case 2:
			// Source sub-package changed → refresh own candidates + update source skill ID candidates
			if srcPkg != "" {
				m.fields[2].SetCandidates(listSubPackages(m.basePath, srcPkg))
				m.fields[3].SetCandidates(listSkillIDs(m.basePath, srcPkg, srcSub))
			} else {
				m.fields[2].SetCandidates(listSubPackagesAll(m.basePath))
				m.fields[3].SetCandidates(m.skillIDsAll())
			}
		case 3:
			// Source skill ID focused → ensure candidates are correct for current pkg+sub
			if srcPkg != "" {
				m.fields[3].SetCandidates(listSkillIDs(m.basePath, srcPkg, srcSub))
			} else {
				m.fields[3].SetCandidates(m.skillIDsAll())
			}
		case 4:
			// Dest sub-package focused → ensure candidates are correct for current dstPkg
			if dstPkg != "" {
				m.fields[4].SetCandidates(listSubPackages(m.basePath, dstPkg))
			} else {
				m.fields[4].SetCandidates(listSubPackagesAll(m.basePath))
			}
		}

	case ModeSkillTest:
		srcPkg := m.fields[0].input.Value()
		srcSub := m.fields[1].input.Value()
		switch fieldIdx {
		case 0:
			// 包名变化 → 更新小包与技能ID候选
			if srcPkg != "" {
				m.fields[1].SetCandidates(listSubPackages(m.basePath, srcPkg))
				m.fields[2].SetCandidates(listSkillIDs(m.basePath, srcPkg, srcSub))
			} else {
				m.fields[1].SetCandidates(listSubPackagesAll(m.basePath))
				m.fields[2].SetCandidates(m.skillIDsAll())
			}
		case 1:
			// 小包变化 → 刷新自身候选 + 更新技能ID候选
			if srcPkg != "" {
				m.fields[1].SetCandidates(listSubPackages(m.basePath, srcPkg))
				m.fields[2].SetCandidates(listSkillIDs(m.basePath, srcPkg, srcSub))
			} else {
				m.fields[1].SetCandidates(listSubPackagesAll(m.basePath))
				m.fields[2].SetCandidates(m.skillIDsAll())
			}
		case 2:
			// 技能ID聚焦 → 确保候选正确
			if srcPkg != "" {
				m.fields[2].SetCandidates(listSkillIDs(m.basePath, srcPkg, srcSub))
			} else {
				m.fields[2].SetCandidates(m.skillIDsAll())
			}
		}
	}
}

// listSubPackages returns sub-package directory names under a package's pkg/.
func listSubPackages(base, pkg string) []string {
	dir := filepath.Join(base, packagesDir, pkg, "pkg")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var subs []string
	for _, e := range entries {
		if e.IsDir() {
			subs = append(subs, e.Name())
		}
	}
	sort.Strings(subs)
	return subs
}

// listSubPackagesAll returns all sub-packages across all packages.
func listSubPackagesAll(base string) []string {
	seen := map[string]bool{}
	pkgs := listPackages(base)
	for _, pkg := range pkgs {
		for _, s := range listSubPackages(base, pkg) {
			seen[s] = true
		}
	}
	var result []string
	for k := range seen {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// skillIDPattern matches `name = "something"` in skill files.
var skillIDPattern = regexp.MustCompile(`(?m)^\s*name\s*=\s*"([^"]+)"`)

// listSkillIDs returns unique skill IDs from skill files.
// If subPkg is empty, scans all sub-packages; otherwise only the given one.
func listSkillIDs(base, pkg, subPkg string) []string {
	seen := map[string]bool{}
	pkgDir := filepath.Join(base, packagesDir, pkg, "pkg")

	var dirs []string
	if subPkg != "" {
		dirs = []string{subPkg}
	} else {
		entries, err := os.ReadDir(pkgDir)
		if err != nil {
			return nil
		}
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, e.Name())
			}
		}
	}

	for _, sub := range dirs {
		skillDir := filepath.Join(pkgDir, sub, "skills")
		entries, err := os.ReadDir(skillDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".lua") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(skillDir, e.Name()))
			if err != nil {
				continue
			}
			m := skillIDPattern.FindSubmatch(data)
			if len(m) == 2 {
				seen[string(m[1])] = true
			}
		}
	}

	var result []string
	for k := range seen {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// listSkillIDsAll returns all skill IDs across all packages/sub-packages.
func listSkillIDsAll(base string) []string {
	seen := map[string]bool{}
	pkgs := listPackages(base)
	for _, pkg := range pkgs {
		for _, id := range listSkillIDs(base, pkg, "") {
			seen[id] = true
		}
	}
	var result []string
	for k := range seen {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// findSkillFile finds the source .lua file for a given skill ID.
func findSkillFile(base, pkg, skillID string) (string, string, error) {
	pkgDir := filepath.Join(base, packagesDir, pkg, "pkg")
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return "", "", fmt.Errorf("无法读取 %s/pkg/: %w", pkg, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillDir := filepath.Join(pkgDir, e.Name(), "skills")
		fileEntries, err := os.ReadDir(skillDir)
		if err != nil {
			continue
		}
		for _, fe := range fileEntries {
			if fe.IsDir() || !strings.HasSuffix(fe.Name(), ".lua") {
				continue
			}
			fullPath := filepath.Join(skillDir, fe.Name())
			data, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}
			m := skillIDPattern.FindSubmatch(data)
			if len(m) == 2 && string(m[1]) == skillID {
				return fullPath, e.Name(), nil
			}
		}
	}
	return "", "", fmt.Errorf("在 %s 中找不到技能 '%s' 的文件", pkg, skillID)
}

// doCopySkillFile copies a skill .lua file and replaces the name line.
func doCopySkillFile(base, srcPkg, dstPkg, dstSubPkg, srcSkillID, dstSkillID string) (string, error) {
	srcPath, _, err := findSkillFile(base, srcPkg, srcSkillID)
	if err != nil {
		return "", err
	}

	dstDir := filepath.Join(base, packagesDir, dstPkg, "pkg", dstSubPkg, "skills")
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return "", fmt.Errorf("读取源文件失败: %w", err)
	}

	content := strings.ReplaceAll(string(data), srcSkillID, dstSkillID)

	_, srcName := filepath.Split(srcPath)
	dstName := strings.ReplaceAll(srcName, srcSkillID, dstSkillID)
	dstPath := filepath.Join(dstDir, dstName)

	if err := os.WriteFile(dstPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("写入目标文件失败: %w", err)
	}

	return fmt.Sprintf("已拷贝: %s → %s", srcName, dstName), nil
}

// ============================================================
// Copy logic
// ============================================================

// findPortraitFile finds the portrait file for a general (full stem, e.g. "chunyu1__huangwudie") in a package.
func findPortraitFile(base, pkg, general string) (string, error) {
	imgDir := filepath.Join(base, packagesDir, pkg, "image", "generals")
	entries, err := os.ReadDir(imgDir)
	if err != nil {
		return "", fmt.Errorf("无法读取 %s/image/generals/: %w", pkg, err)
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if name == general {
			return filepath.Join(imgDir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("在 %s 中找不到武将 '%s' 的原画", pkg, general)
}

// findDeathFile finds the death voice file for a general (full stem) in a package.
func findDeathFile(base, pkg, general string) (string, error) {
	deathDir := filepath.Join(base, packagesDir, pkg, "audio", "death")
	entries, err := os.ReadDir(deathDir)
	if err != nil {
		return "", fmt.Errorf("无法读取 %s/audio/death/: %w", pkg, err)
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if name == general {
			return filepath.Join(deathDir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("在 %s 中找不到武将 '%s' 的阵亡语音", pkg, general)
}

// findSkillFiles finds all numbered skill voice files for a skill in a package.
func findSkillFiles(base, pkg, skill string) (map[string]string, error) {
	skillDir := filepath.Join(base, packagesDir, pkg, "audio", "skill")
	entries, err := os.ReadDir(skillDir)
	if err != nil {
		return nil, fmt.Errorf("无法读取 %s/audio/skill/: %w", pkg, err)
	}

	pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(skill) + `(\d+)\.mp3$`)
	result := make(map[string]string)
	for _, e := range entries {
		m := pattern.FindStringSubmatch(e.Name())
		if len(m) == 2 {
			result[m[1]] = filepath.Join(skillDir, e.Name())
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("在 %s 中找不到技能 '%s' 的语音", pkg, skill)
	}
	return result, nil
}

// copyFile copies a file from src to dst, creating parent directories.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	srcF, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcF.Close()

	dstF, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstF.Close()

	_, err = io.Copy(dstF, srcF)
	return err
}

// doCopyPortrait copies a portrait from srcPkg/srcGen to dstPkg/dstGen.
func doCopyPortrait(base, srcPkg, dstPkg, srcGen, dstGen string) error {
	srcPath, err := findPortraitFile(base, srcPkg, srcGen)
	if err != nil {
		return err
	}
	ext := filepath.Ext(srcPath)
	dstPath := filepath.Join(base, packagesDir, dstPkg, "image", "generals", dstGen+ext)
	return copyFile(srcPath, dstPath)
}

// doCopyDeath copies a death voice from srcPkg/srcGen to dstPkg/dstGen.
func doCopyDeath(base, srcPkg, dstPkg, srcGen, dstGen string) error {
	srcPath, err := findDeathFile(base, srcPkg, srcGen)
	if err != nil {
		return err
	}
	ext := filepath.Ext(srcPath)
	dstPath := filepath.Join(base, packagesDir, dstPkg, "audio", "death", dstGen+ext)
	return copyFile(srcPath, dstPath)
}

// doCopySkill copies all skill voice files from srcPkg/srcSkill to dstPkg/dstSkill.
func doCopySkill(base, srcPkg, dstPkg, srcSkill, dstSkill string) (int, error) {
	numMap, err := findSkillFiles(base, srcPkg, srcSkill)
	if err != nil {
		return 0, err
	}

	count := 0
	dstDir := filepath.Join(base, packagesDir, dstPkg, "audio", "skill")
	for num, srcPath := range numMap {
		dstName := dstSkill + num + ".mp3"
		dstPath := filepath.Join(dstDir, dstName)
		if err := copyFile(srcPath, dstPath); err != nil {
			return count, fmt.Errorf("拷贝 %s 失败: %w", num, err)
		}
		count++
	}
	return count, nil
}

// doDownload downloads an mp3 from the URL and saves to the package's audio dir.
// 文件名 = 填入的语音标识 + 编号(技能) + 扩展名,不再额外拼接包名前缀。
func doDownload(base, pkg, voiceType, skillID, index, downloadURL string) (string, error) {
	var subdir, filename string
	if voiceType == "阵亡语音" {
		subdir = "death"
		filename = skillID + ".mp3"
	} else {
		subdir = "skill"
		filename = skillID + index + ".mp3"
	}

	dstDir := filepath.Join(base, packagesDir, pkg, "audio", subdir)
	dstPath := filepath.Join(dstDir, filename)

	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	_, err := url.Parse(downloadURL)
	if err != nil {
		return "", fmt.Errorf("无效的下载链接: %w", err)
	}

	resp, err := http.Get(downloadURL)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("服务器返回 %d", resp.StatusCode)
	}

	f, err := os.Create(dstPath)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
	}
	defer f.Close()

	written, err := io.Copy(f, resp.Body)
	if err != nil {
		return "", fmt.Errorf("写入文件失败: %w", err)
	}

	return fmt.Sprintf("已下载 → %s/%s (%d bytes)", subdir, filename, written), nil
}

// ============================================================
// Skill test (FreeKill -testfile)
// ============================================================

// skillTestMsg 承载技能测试命令的最终输出。
type skillTestMsg struct {
	skillName string
	output    string
	err       error
}

// testTickMsg 驱动测试运行中界面的 spinner 动画。
type testTickMsg struct{}

var testFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func testTickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return testTickMsg{}
	})
}

// stripANSI 去除输出中的 ANSI 颜色/控制转义序列,便于在纯文本界面中显示。
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// summarizeSkillTest 从 FreeKill 测试输出中提取结果摘要。
func summarizeSkillTest(output string) string {
	var summary []string
	for _, line := range strings.Split(output, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "Ran ") || t == "OK" || strings.HasPrefix(t, "Failed tests:") ||
			strings.HasPrefix(t, "1)") || strings.HasPrefix(t, "FAIL") ||
			strings.Contains(t, "expected:") || strings.HasPrefix(t, "stack traceback:") ||
			strings.Contains(t, "attempt to") || strings.Contains(t, ".lua:") {
			summary = append(summary, t)
		}
	}
	if len(summary) == 0 {
		return "无测试输出"
	}
	return strings.Join(summary, "\n")
}

// runSkillTestCmd 构造运行技能测试的命令(由 tea.ExecProcess 移交终端执行)。
// stdin 保持为 nil 以继承真实终端,这样 Lua 文件中的 dbg() 断点可以直接像在
// 终端里运行那样进行输入交互;stdout/stderr 同时写入终端与缓冲区,
// 缓冲区内容供测试完成后的结果查看页使用。
func runSkillTestCmd(base, skillFile string) (*exec.Cmd, *bytes.Buffer, error) {
	fkBin := filepath.Join(base, "FreeKill")
	if _, err := os.Stat(fkBin); err != nil {
		return nil, nil, fmt.Errorf("未找到 FreeKill 可执行文件: %v", err)
	}
	cmd := exec.Command(fkBin, "-testfile", skillFile)
	cmd.Dir = base
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	return cmd, &buf, nil
}

// ============================================================
// Quick-start config (quick_start.json)
// ============================================================

// arrayPropKeys 是 Lua 侧按数组遍历的玩家属性字段,单值也要包成数组。
var arrayPropKeys = map[string]bool{
	"skills": true,
	"cards":  true,
	"equips": true,
}

// inferProp 把 "true"/"false"/数字/逗号分隔列表 转为对应类型,否则原样返回字符串。
func inferProp(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	if strings.Contains(v, ",") {
		parts := strings.Split(v, ",")
		arr := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				arr = append(arr, p)
			}
		}
		return arr
	}
	return v
}

// playerAt 返回 players[seat] 的 map,不存在则创建。
func playerAt(players map[string]any, seat string) map[string]any {
	p, ok := players[seat].(map[string]any)
	if !ok {
		p = map[string]any{}
		players[seat] = p
	}
	return p
}

// parseSeatMap 解析 "1:caocao, 2:guanyu" 形式的座位→值配置。
func parseSeatMap(s string, fn func(seat, val string)) {
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		seat, val, ok := strings.Cut(item, ":")
		if !ok {
			continue
		}
		fn(strings.TrimSpace(seat), strings.TrimSpace(val))
	}
}

// parseSeatProps 解析 "1:hp=4; 1:cards=slash,crossbow" 形式的座位属性配置。
// 多个配置用 ; 分隔(值内逗号表示数组),避免与数组分隔符冲突。
func parseSeatProps(s string, fn func(seat, key string, val any)) {
	for _, item := range strings.Split(s, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		seat, rest, ok := strings.Cut(item, ":")
		if !ok {
			continue
		}
		key, raw, ok := strings.Cut(strings.TrimSpace(rest), "=")
		if !ok {
			continue
		}
		fn(strings.TrimSpace(seat), strings.TrimSpace(key), inferProp(strings.TrimSpace(raw)))
	}
}

// defaultRoles 每局玩家数对应的默认身份表（参考 ltk/server/gamelogic.lua 的 role_table）。
var defaultRoles = [][]string{
	{"lord"},
	{"lord", "rebel"},
	{"lord", "rebel", "renegade"},
	{"lord", "loyalist", "rebel", "renegade"},
	{"lord", "loyalist", "rebel", "rebel", "renegade"},
	{"lord", "loyalist", "rebel", "rebel", "rebel", "renegade"},
	{"lord", "loyalist", "loyalist", "rebel", "rebel", "rebel", "renegade"},
	{"lord", "loyalist", "loyalist", "rebel", "rebel", "rebel", "rebel", "renegade"},
}

// buildQuickStartConfig 根据表单字段构建 quick_start.json 配置。
// 未指定武将的座位:第一个补谋徐盛(mouxusheng),其余补士兵(blank_shibing)。
func buildQuickStartConfig(vals []string) (map[string]any, error) {
	mode := strings.TrimSpace(vals[0])
	if mode == "" {
		mode = "aaa_role_mode"
	}
	numStr := strings.TrimSpace(vals[1])
	if numStr == "" {
		numStr = "2"
	}
	num, err := strconv.Atoi(numStr)
	if err != nil || num < 1 {
		return nil, fmt.Errorf("玩家人数无效: %s", numStr)
	}

	players := map[string]any{}

	parseSeatMap(vals[2], func(seat, val string) {
		if val != "" {
			playerAt(players, seat)["general"] = val
		}
	})
	parseSeatMap(vals[3], func(seat, val string) {
		if val != "" {
			playerAt(players, seat)["role"] = val
		}
	})
	parseSeatProps(vals[4], func(seat, key string, val any) {
		p := playerAt(players, seat)
		if arrayPropKeys[key] {
			if s, ok := val.(string); ok {
				val = []string{s}
			}
		}
		p[key] = val
	})

	// 补全未指定武将的座位:第一个谋徐盛,其余士兵
	filledMou := false
	for seat := 1; seat <= num; seat++ {
		s := strconv.Itoa(seat)
		p := playerAt(players, s)
		if g, _ := p["general"].(string); g == "" {
			if !filledMou {
				p["general"] = "mouxusheng"
				filledMou = true
			} else {
				p["general"] = "blank_shibing"
			}
		}
	}

	// 补全未指定身份的座位:按默认身份表(defaultRoles)顺序分配
	if num <= len(defaultRoles) {
		for seat := 1; seat <= num; seat++ {
			p := playerAt(players, strconv.Itoa(seat))
			if r, _ := p["role"].(string); r == "" && seat <= len(defaultRoles[num-1]) {
				p["role"] = defaultRoles[num-1][seat-1]
			}
		}
	}

	// 默认 1 号位(自己)控制所有其他角色,除非显式关闭
	if strings.TrimSpace(vals[5]) != "false" {
		p1 := playerAt(players, "1")
		if _, ok := p1["controlOther"]; !ok {
			p1["controlOther"] = true
		}
	}

	return map[string]any{
		"gameMode":  mode,
		"playerNum": num,
		"players":   players,
	}, nil
}

// ============================================================
// Bubble Tea interface
// ============================================================

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case testTickMsg:
		// 驱动测试运行中的 spinner 动画
		if m.stage == StageTestRunning {
			m.testFrame = (m.testFrame + 1) % len(testFrames)
			return m, testTickCmd()
		}
		return m, nil

	case skillTestMsg:
		// 技能测试完成:保存完整输出,进入独立输出查看页
		m.testOutput = stripANSI(msg.output)
		m.testOutputScroll = 0
		if msg.err != nil {
			m.testOutputOK = false
			m.status = "测试运行失败: " + msg.err.Error()
			m.statusOK = false
		} else if strings.Contains(msg.output, "\nOK\n") || strings.HasPrefix(strings.TrimSpace(msg.output), "OK") ||
			strings.Contains(msg.output, "0 failures") {
			m.testOutputOK = true
			m.status = "✅ " + msg.skillName + " 测试通过"
			m.statusOK = true
		} else {
			m.testOutputOK = false
			m.status = "❌ " + msg.skillName + " 测试失败"
			m.statusOK = false
		}
		m.stage = StageTestOutput
		return m, nil

	case tea.KeyMsg:
		switch m.stage {
		case StageModeSelect:
			return m.updateModeSelect(msg)
		case StageForm:
			return m.updateForm(msg)
		case StageConfirm:
			return m.updateConfirm(msg)
		case StageDownload:
			return m.updateDownload(msg)
		case StageTestRunning:
			// 测试运行中:仅允许 Q 退出,其余按键忽略
			if msg.String() == "q" {
				return m, tea.Quit
			}
		case StageTestOutput:
			// 输出查看页:滚动查看,退出返回表单
			switch msg.String() {
			case "up":
				if m.testOutputScroll > 0 {
					m.testOutputScroll--
				}
			case "down":
				m.testOutputScroll++
			case "pgup":
				m.testOutputScroll -= max(5, m.height-10)
				if m.testOutputScroll < 0 {
					m.testOutputScroll = 0
				}
			case "pgdown":
				m.testOutputScroll += max(5, m.height-10)
			case "home":
				m.testOutputScroll = 0
			case "end":
				m.testOutputScroll = math.MaxInt32 // 视图中会被限制到末尾
			case "esc", "q":
				m.stage = StageForm
			}
		}
	}
	return m, nil
}

func (m model) updateModeSelect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "left", "h":
		if m.mode > 0 {
			m.mode--
		} else {
			// 循环:最左再往左回到最后一个模式
			m.mode = ModeQuickStart
		}
	case "right", "l":
		if m.mode < ModeQuickStart {
			m.mode++
		} else {
			// 循环:最右再往右回到第一个模式
			m.mode = ModePortrait
		}
	case "enter":
		m.setFieldsForMode()
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	active := m.fields[m.activeField]

	switch msg.String() {
	case "ctrl+a":
		// Clear current line content
		active.input.SetValue("")
		active.input.CursorEnd()

	case "tab":
		m.refreshCompletions(m.activeField)
		if active.showDropdown {
			active.FuzzyFilter()
			if len(active.filtered) > 0 {
				active.cursor = (active.cursor + 1) % len(active.filtered)
			}
		} else {
			active.FuzzyFilter()
			if len(active.filtered) == 1 {
				active.input.SetValue(active.filtered[0])
				active.input.CursorEnd()
				active.showDropdown = false
			} else if len(active.filtered) > 1 {
				active.showDropdown = true
				active.cursor = 0
			}
		}

	case "esc":
		if active.showDropdown {
			active.showDropdown = false
			active.cursor = 0
		} else {
			m.saveToCache()
			m.stage = StageModeSelect
			m.status = ""
		}

	case "up":
		if active.showDropdown {
			if active.cursor > 0 {
				active.cursor--
			}
			return m, nil
		}
		if m.activeField > 0 {
			active.Blur()
			m.activeField--
			m.fields[m.activeField].Focus()
		}

	case "down":
		if active.showDropdown {
			if active.cursor < len(active.filtered)-1 {
				active.cursor++
			}
			return m, nil
		}
		if m.activeField < len(m.fields)-1 {
			active.Blur()
			m.activeField++
			m.fields[m.activeField].Focus()
		}

	case "enter":
		if active.showDropdown {
			active.AcceptSelection()
		} else {
			if m.activeField < len(m.fields)-1 {
				active.Blur()
				m.activeField++
				m.fields[m.activeField].Focus()
			} else if m.formCanSubmit() {
				m.stage = StageConfirm
			} else {
				for i, f := range m.fields {
					if strings.TrimSpace(f.input.Value()) == "" {
						active.Blur()
						m.activeField = i
						m.fields[i].Focus()
						break
					}
				}
			}
		}

	case "ctrl+s":
		if m.formCanSubmit() {
			m.stage = StageConfirm
		} else {
			m.status = "请填写所有必填字段后再提交"
			m.statusOK = false
		}

	default:
		var cmd tea.Cmd
		active.input, cmd = active.input.Update(msg)
		if active.showDropdown {
			active.FuzzyFilter()
			if len(active.filtered) == 0 {
				active.showDropdown = false
			}
			if active.cursor >= len(active.filtered) {
				active.cursor = 0
			}
		}
		return m, cmd
	}

	return m, nil
}

func (m model) updateDownload(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	active := m.fields[m.activeField]

	switch msg.String() {
	case "ctrl+a":
		active.input.SetValue("")
		active.input.CursorEnd()

	case "tab":
		if m.activeField == 1 {
			// Toggle voice type
			if active.input.Value() == "技能语音" {
				active.input.SetValue("阵亡语音")
			} else {
				active.input.SetValue("技能语音")
			}
		} else if m.activeField == 0 {
			// Package completion
			active.FuzzyFilter()
			if active.showDropdown {
				active.FuzzyFilter()
				if len(active.filtered) > 0 {
					active.cursor = (active.cursor + 1) % len(active.filtered)
				}
			} else {
				if len(active.filtered) == 1 {
					active.input.SetValue(active.filtered[0])
					active.input.CursorEnd()
					active.showDropdown = false
				} else if len(active.filtered) > 1 {
					active.showDropdown = true
					active.cursor = 0
				}
			}
		}

	case "esc":
		if active.showDropdown {
			active.showDropdown = false
			active.cursor = 0
		} else {
			m.stage = StageModeSelect
			m.status = ""
		}

	case "up":
		if active.showDropdown && m.activeField == 0 {
			if active.cursor > 0 {
				active.cursor--
			}
			return m, nil
		}
		if m.activeField > 0 {
			active.Blur()
			m.activeField--
			m.fields[m.activeField].Focus()
		}

	case "down":
		if active.showDropdown && m.activeField == 0 {
			if active.cursor < len(active.filtered)-1 {
				active.cursor++
			}
			return m, nil
		}
		if m.activeField < len(m.fields)-1 {
			active.Blur()
			m.activeField++
			m.fields[m.activeField].Focus()
		}

	case "enter":
		if active.showDropdown && m.activeField == 0 {
			active.AcceptSelection()
		} else if m.activeField < len(m.fields)-1 {
			active.Blur()
			m.activeField++
			m.fields[m.activeField].Focus()
		} else {
			// Submit: validate all fields
			pkg := strings.TrimSpace(m.fields[0].input.Value())
			voiceType := strings.TrimSpace(m.fields[1].input.Value())
			skillID := strings.TrimSpace(m.fields[2].input.Value())
			index := strings.TrimSpace(m.fields[3].input.Value())
			downloadURL := strings.TrimSpace(m.fields[4].input.Value())

			if pkg == "" {
				m.status = "目标包名不能为空"
				m.statusOK = false
			} else if skillID == "" {
				m.status = "语音标识不能为空"
				m.statusOK = false
			} else if downloadURL == "" {
				m.status = "下载链接不能为空"
				m.statusOK = false
			} else {
				result, err := doDownload(m.basePath, pkg, voiceType, skillID, index, downloadURL)
				if err != nil {
					m.status = "下载失败: " + err.Error()
					m.statusOK = false
				} else {
					m.status = result
					m.statusOK = true
				}
				m.fields[3].input.SetValue("")
				m.fields[4].input.SetValue("")
				m.activeField = 3
				m.fields[3].Focus()
			}
		}

	default:
		var cmd tea.Cmd
		active.input, cmd = active.input.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "y":
		// 仅快速启动模式需要启动 FreeKill,其余模式不得残留该标志
		m.quickStartLaunch = m.mode == ModeQuickStart
		return m.execute()
	case "ctrl+s":
		// 仅写入配置,不启动 FreeKill
		m.quickStartLaunch = false
		return m.execute()
	case "n", "esc":
		m.stage = StageForm
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m model) execute() (tea.Model, tea.Cmd) {
	vals := m.values()

	if m.mode == ModeSkillFile {
		srcPkg, dstPkg := vals[0], vals[1]
		srcID := vals[3]
		dstSub, dstID := vals[4], vals[5]

		result, err := doCopySkillFile(m.basePath, srcPkg, dstPkg, dstSub, srcID, dstID)
		if err != nil {
			m.status = "错误: " + err.Error()
			m.statusOK = false
		} else {
			m.status = result
			m.statusOK = true
		}
		// Clear skill IDs, keep pkgs and sub-pkgs
		m.fields[3].input.SetValue("")
		m.fields[5].input.SetValue("")
		m.activeField = 3
		m.fields[3].Focus()
		m.stage = StageForm
		return m, nil
	}

	if m.mode == ModeQuickStart {
		cfg, err := buildQuickStartConfig(vals)
		if err != nil {
			m.status = "错误: " + err.Error()
			m.statusOK = false
			m.stage = StageForm
			return m, nil
		}
		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			m.status = "错误: " + err.Error()
			m.statusOK = false
			m.stage = StageForm
			return m, nil
		}
		dstPath := filepath.Join(m.basePath, "quick_start.json")
		if err := os.WriteFile(dstPath, append(data, '\n'), 0644); err != nil {
			m.status = "写入失败: " + err.Error()
			m.statusOK = false
			m.stage = StageForm
			return m, nil
		}
		m.status = "已写入 quick_start.json"
		m.statusOK = true
		if m.quickStartLaunch {
			return m, tea.Quit
		}
		m.stage = StageForm
		return m, nil
	}

	if m.mode == ModeSkillTest {
		srcPkg := strings.TrimSpace(vals[0])
		skillID := strings.TrimSpace(vals[2])
		filePath, _, err := findSkillFile(m.basePath, srcPkg, skillID)
		if err != nil {
			m.status = "错误: " + err.Error()
			m.statusOK = false
			m.stage = StageForm
			return m, nil
		}
		cmd, buf, err := runSkillTestCmd(m.basePath, filePath)
		if err != nil {
			m.status = "错误: " + err.Error()
			m.statusOK = false
			m.stage = StageForm
			return m, nil
		}
		// 切换到测试运行中界面,并把终端移交给测试进程:
		// 这样 Lua 中的 dbg() 断点可以直接在终端里输入交互,
		// 进程结束后自动恢复 TUI 并显示捕获到的输出。
		m.testSkillName = skillID
		m.testFilePath = filePath
		m.testFrame = 0
		m.stage = StageTestRunning
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return skillTestMsg{skillName: skillID, output: buf.String(), err: err}
		})
	}

	srcPkg, dstPkg, srcName, dstName := vals[0], vals[1], vals[2], vals[3]

	var err error
	switch m.mode {
	case ModePortrait:
		err = doCopyPortrait(m.basePath, srcPkg, dstPkg, srcName, dstName)
		if err == nil {
			m.status = fmt.Sprintf("已拷贝原画: %s/%s → %s/%s",
				srcPkg, srcName, dstPkg, dstName)
			m.statusOK = true
		}
	case ModeDeath:
		err = doCopyDeath(m.basePath, srcPkg, dstPkg, srcName, dstName)
		if err == nil {
			m.status = fmt.Sprintf("已拷贝阵亡语音: %s/%s → %s/%s",
				srcPkg, srcName, dstPkg, dstName)
			m.statusOK = true
		}
	case ModeSkill:
		n, e := doCopySkill(m.basePath, srcPkg, dstPkg, srcName, dstName)
		if e != nil {
			err = e
		} else {
			m.status = fmt.Sprintf("已拷贝技能语音: %s/%s* (%d 个) → %s/%s*",
				srcPkg, srcName, n, dstPkg, dstName)
			m.statusOK = true
		}
	}

	if err != nil {
		m.status = "错误: " + err.Error()
		m.statusOK = false
	}

	m.stage = StageForm
	return m, nil
}

// ============================================================
// View - rendering
// ============================================================

func (m model) View() string {
	var b strings.Builder

	switch m.stage {
	case StageModeSelect:
		b.WriteString(m.viewModeSelect())
	case StageForm:
		b.WriteString(m.viewForm())
	case StageConfirm:
		b.WriteString(m.viewConfirm())
	case StageDownload:
		b.WriteString(m.viewDownload())
	case StageTestRunning:
		b.WriteString(m.viewTestRunning())
	case StageTestOutput:
		b.WriteString(m.viewTestOutput())
	}

	b.WriteString("\n")
	b.WriteString(styleHelp.Render(m.helpText()))
	return b.String()
}

// viewTestOutput 渲染技能测试的完整输出查看页。
func (m model) viewTestOutput() string {
	var b strings.Builder

	if m.testOutputOK {
		b.WriteString(styleTitle.Render("✅ 测试通过 - 完整输出 (" + m.testSkillName + ")"))
	} else {
		b.WriteString(styleTitle.Render("❌ 测试失败 - 完整输出 (" + m.testSkillName + ")"))
	}
	b.WriteString("\n")

	lines := strings.Split(m.testOutput, "\n")
	// 去掉尾部的空行
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	availHeight := m.height - 5
	if availHeight < 8 {
		availHeight = 8
	}

	// 限制滚动范围
	maxScroll := len(lines) - availHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.testOutputScroll > maxScroll {
		m.testOutputScroll = maxScroll
	}

	start := m.testOutputScroll
	end := start + availHeight
	if end > len(lines) {
		end = len(lines)
	}
	for i := start; i < end; i++ {
		b.WriteString(lines[i])
		b.WriteString("\n")
	}
	if len(lines) == 0 {
		b.WriteString(styleDim.Render("(无输出)"))
	}

	if len(lines) > availHeight {
		b.WriteString(styleDim.Render(fmt.Sprintf("\n  显示 %d-%d / %d 行", start+1, end, len(lines))))
	}
	return b.String()
}

// viewTestRunning 渲染技能测试运行中的界面。
func (m model) viewTestRunning() string {
	var b strings.Builder
	frame := testFrames[m.testFrame%len(testFrames)]

	b.WriteString(styleTitle.Render("测试技能 - 运行中"))
	b.WriteString("\n\n")
	b.WriteString(stylePrompt.Render(frame + " 测试已提交，正在执行，请勿关闭窗口..."))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  技能: %s\n", m.testSkillName))
	b.WriteString(fmt.Sprintf("  文件: %s\n", m.testFilePath))
	b.WriteString("  命令: ./FreeKill -testfile <技能文件>\n")
	b.WriteString("\n")
	b.WriteString(styleDim.Render("测试已移交终端执行：若 Lua 文件中有 dbg() 断点，"))
	b.WriteString(styleDim.Render("会像在终端里直接运行一样停下来等待输入 (h=帮助, c=继续, q=退出调试)。"))
	b.WriteString("\n")
	b.WriteString(styleDim.Render("正在加载全部拓展包并运行该技能的内嵌测试(含 setUp/tearDown)，"))
	b.WriteString(styleDim.Render("通常需要 10~30 秒，请耐心等待。完成后会自动返回并显示结果。"))
	return b.String()
}

func (m model) viewModeSelect() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("FK 资源拷贝工具"))
	b.WriteString("\n\n")
	b.WriteString(styleDim.Render("选择模式 (← → 切换, Enter 确认):"))
	b.WriteString("\n\n")

	for i, label := range modeLabels {
		if Mode(i) == m.mode {
			b.WriteString(styleModeActive.Render("▶ " + label))
		} else {
			b.WriteString(styleModeInactive.Render("  " + label))
		}
		if i < len(modeLabels)-1 {
			b.WriteString("  ")
		}
	}

	return b.String()
}

func (m model) viewForm() string {
	var b strings.Builder

	modeStr := modeLabels[m.mode]
	b.WriteString(styleTitle.Render("FK 资源拷贝 - " + modeStr))
	b.WriteString("\n\n")

	for i, f := range m.fields {
		isActive := i == m.activeField

		label := f.label
		if isActive {
			label = stylePrompt.Render("▶ ") + styleLabel.Render(label)
		} else {
			label = "  " + styleLabel.Render(label)
		}
		b.WriteString(label)
		b.WriteString(": ")
		b.WriteString(f.input.View())
		b.WriteString("\n")

		if isActive && f.showDropdown && len(f.filtered) > 0 {
			maxShow := 6
			start := f.cursor - 2
			if start < 0 {
				start = 0
			}
			end := start + maxShow
			if end > len(f.filtered) {
				end = len(f.filtered)
				start = end - maxShow
				if start < 0 {
					start = 0
				}
			}

			prefix := "       "
			for j := start; j < end; j++ {
				entry := f.filtered[j]
				if j == f.cursor {
					b.WriteString(prefix + styleDropdownSel.Render(entry) + "\n")
				} else {
					b.WriteString(prefix + styleDropdown.Render(entry) + "\n")
				}
			}
		}
	}

	if m.status != "" {
		b.WriteString("\n")
		if m.statusOK {
			b.WriteString(styleSuccess.Render(m.status))
		} else {
			b.WriteString(styleError.Render(m.status))
		}
	}

	return b.String()
}

func (m model) viewDownload() string {
	var b strings.Builder

	b.WriteString(styleTitle.Render("FK 语音下载"))
	b.WriteString("\n\n")

	for i, f := range m.fields {
		isActive := i == m.activeField

		label := f.label
		if isActive {
			label = stylePrompt.Render("▶ ") + styleLabel.Render(label)
		} else {
			label = "  " + styleLabel.Render(label)
		}
		b.WriteString(label)
		b.WriteString(": ")
		b.WriteString(f.input.View())
		b.WriteString("\n")

		if isActive && f.showDropdown && len(f.filtered) > 0 {
			maxShow := 6
			start := f.cursor - 2
			if start < 0 {
				start = 0
			}
			end := start + maxShow
			if end > len(f.filtered) {
				end = len(f.filtered)
				start = end - maxShow
				if start < 0 {
					start = 0
				}
			}

			prefix := "       "
			for j := start; j < end; j++ {
				entry := f.filtered[j]
				if j == f.cursor {
					b.WriteString(prefix + styleDropdownSel.Render(entry) + "\n")
				} else {
					b.WriteString(prefix + styleDropdown.Render(entry) + "\n")
				}
			}
		}
	}

	if m.status != "" {
		b.WriteString("\n")
		if m.statusOK {
			b.WriteString(styleSuccess.Render(m.status))
		} else {
			b.WriteString(styleError.Render(m.status))
		}
	}

	return b.String()
}

// seatSummary 生成单个座位的分配摘要,如 "caocao (lord, hp=4, 全控)"。
func seatSummary(players map[string]any, seat int) string {
	pm, ok := players[strconv.Itoa(seat)].(map[string]any)
	if !ok {
		return "?"
	}
	gen, _ := pm["general"].(string)
	if gen == "" {
		gen = "?"
	}
	var attrs []string
	if r, ok := pm["role"].(string); ok && r != "" {
		attrs = append(attrs, r)
	}
	if co, ok := pm["controlOther"].(bool); ok && co {
		attrs = append(attrs, "全控")
	}
	keys := make([]string, 0, len(pm))
	for k := range pm {
		switch k {
		case "general", "role", "controlOther":
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if arr, ok := pm[k].([]string); ok {
			attrs = append(attrs, fmt.Sprintf("%s=[%s]", k, strings.Join(arr, ",")))
		} else {
			attrs = append(attrs, fmt.Sprintf("%s=%v", k, pm[k]))
		}
	}
	if len(attrs) > 0 {
		gen += " (" + strings.Join(attrs, ", ") + ")"
	}
	return gen
}

func (m model) viewConfirm() string {
	var b strings.Builder
	vals := m.values()

	b.WriteString(styleTitle.Render("确认执行"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  模式: %s\n", modeLabels[m.mode]))

	if m.mode == ModeQuickStart {
		b.WriteString(fmt.Sprintf("  游戏模式: %s\n", vals[0]))
		b.WriteString(fmt.Sprintf("  玩家人数: %s\n", vals[1]))
		cfg, err := buildQuickStartConfig(vals)
		if err != nil {
			b.WriteString(styleError.Render("  配置解析失败: "+err.Error()) + "\n")
		} else {
			players := cfg["players"].(map[string]any)
			num, _ := cfg["playerNum"].(int)
			for seat := 1; seat <= num; seat++ {
				b.WriteString(fmt.Sprintf("  座位%d: %s\n", seat, seatSummary(players, seat)))
			}
		}
	} else if m.mode == ModeSkillTest {
		b.WriteString(fmt.Sprintf("  目标包名: %s\n", vals[0]))
		b.WriteString(fmt.Sprintf("  小包(可选): %s\n", vals[1]))
		b.WriteString(fmt.Sprintf("  技能ID: %s\n", vals[2]))
	} else if m.mode == ModeSkillFile {
		b.WriteString(fmt.Sprintf("  来源包: %s\n", vals[0]))
		b.WriteString(fmt.Sprintf("  目标包: %s\n", vals[1]))
		b.WriteString(fmt.Sprintf("  来源小包(可选): %s\n", vals[2]))
		b.WriteString(fmt.Sprintf("  来源技能ID: %s\n", vals[3]))
		b.WriteString(fmt.Sprintf("  目标小包: %s\n", vals[4]))
		b.WriteString(fmt.Sprintf("  目标技能ID: %s\n", vals[5]))
	} else {
		b.WriteString(fmt.Sprintf("  来源包: %s\n", vals[0]))
		b.WriteString(fmt.Sprintf("  目标包: %s\n", vals[1]))
		if m.mode == ModeSkill {
			b.WriteString(fmt.Sprintf("  来源技能: %s\n", vals[2]))
			b.WriteString(fmt.Sprintf("  目标技能: %s\n", vals[3]))
		} else {
			b.WriteString(fmt.Sprintf("  来源武将: %s\n", vals[2]))
			b.WriteString(fmt.Sprintf("  目标武将: %s\n", vals[3]))
		}
	}
	b.WriteString("\n")
	if m.mode == ModeQuickStart {
		b.WriteString(styleDim.Render("将写入 quick_start.json: Enter/Y=写入并启动 FreeKill, Ctrl+S=仅写入, N/Esc=返回"))
	} else if m.mode == ModeSkillTest {
		b.WriteString(styleDim.Render("将运行 FreeKill -testfile 执行该技能的内嵌测试: Enter/Y=运行, N/Esc=返回"))
	} else {
		b.WriteString(stylePrompt.Render("按 Enter/Y 确认, N/Esc 返回"))
	}

	return b.String()
}

func (m model) helpText() string {
	switch m.stage {
	case StageModeSelect:
		return "←→ 切换模式 | Enter 确认 | Q 退出"
	case StageForm:
		return "Tab 补全 | ↑↓ 切换字段/候选 | Ctrl+A 清空本行 | Enter 提交 | Ctrl+S 直接提交 | Esc 返回主菜单"
	case StageConfirm:
		return "Enter/Y 确认 | Ctrl+S 仅写入不启动 | N/Esc 返回 | Q 退出"
	case StageDownload:
		return "Tab 切换类型 | Enter 下载/下一字段 | Esc 返回主菜单 | Ctrl+A 清空本行"
	case StageTestRunning:
		return "测试已移交终端执行(支持 dbg() 断点交互)，完成后自动返回..."
	case StageTestOutput:
		return "↑↓/PgUp/PgDn 滚动 | Home/End 首尾 | Esc/Q 返回表单"
	}
	return ""
}

// ============================================================
// Main
// ============================================================

func main() {
	base, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if _, err := os.Stat(filepath.Join(base, packagesDir)); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: packages/ directory not found in %s\n", base)
		fmt.Fprintf(os.Stderr, "Please run this tool from the FreeKill project root.\n")
		os.Exit(1)
	}

	m := newModel()
	m.basePath = base

	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// 快速启动模式:确认后写入 quick_start.json 并启动 FreeKill
	if fm, ok := final.(model); ok && fm.quickStartLaunch && fm.mode == ModeQuickStart {
		fkBin := filepath.Join(base, "FreeKill")
		if _, statErr := os.Stat(fkBin); os.IsNotExist(statErr) {
			fmt.Fprintf(os.Stderr, "未找到 FreeKill 可执行文件,请手动运行 ./FreeKill -q\n")
		} else {
			fmt.Println("正在启动 FreeKill 快速启动 (-q)...")
			cmd := exec.Command(fkBin, "-q")
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if runErr := cmd.Run(); runErr != nil {
				fmt.Fprintf(os.Stderr, "启动 FreeKill 失败: %v\n", runErr)
			}
		}
	}
}
