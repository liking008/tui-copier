package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// 回归测试:bubbles v0.19.0 的 placeholderView 用显示宽度切 rune 切片,
// 含中文的 placeholder 聚焦空字段时会越界 panic(见 slice bounds out of range)。
func TestChinesePlaceholderNoPanic(t *testing.T) {
	for _, ph := range []string{
		"输入包名,Tab 补全",
		"默认 aaa_role_mode",
		"如 1:caocao, 2:guanyu;留空自动补谋徐盛+士兵",
		"如 1:hp=4; 1:cards=slash,crossbow(多个用;分隔,值内逗号=数组)",
	} {
		ti := textinput.New()
		ti.Placeholder = ph
		ti.Width = 50
		ti.Focus()
		_ = ti.View() // 不应 panic
	}
}

func TestBuildQuickStartConfig(t *testing.T) {
	// 完整配置:3人,指定武将/身份/属性,全控开启
	vals := []string{
		"aaa_role_mode",
		"3",
		"1:caocao, 2:guanyu",
		"1:lord, 2:rebel",
		"1:hp=4; 1:cards=slash,crossbow; 3:skills=jianxiong",
		"true",
	}
	cfg, err := buildQuickStartConfig(vals)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	t.Logf("config:\n%s", data)

	players := cfg["players"].(map[string]any)
	if cfg["gameMode"] != "aaa_role_mode" || cfg["playerNum"] != 3 {
		t.Errorf("bad top-level: %v", cfg)
	}
	p1 := players["1"].(map[string]any)
	if p1["general"] != "caocao" || p1["role"] != "lord" || p1["hp"] != 4 {
		t.Errorf("bad player1: %v", p1)
	}
	if p1["controlOther"] != true {
		t.Errorf("player1 should control others: %v", p1)
	}
	cards, ok := p1["cards"].([]string)
	if !ok || len(cards) != 2 || cards[0] != "slash" || cards[1] != "crossbow" {
		t.Errorf("bad player1 cards: %v", p1["cards"])
	}
	p2 := players["2"].(map[string]any)
	if p2["general"] != "guanyu" || p2["role"] != "rebel" {
		t.Errorf("bad player2: %v", p2)
	}
	p3 := players["3"].(map[string]any)
	if p3["general"] != "mouxusheng" {
		t.Errorf("seat3 should default to mouxusheng: %v", p3)
	}
	skills, ok := p3["skills"].([]string)
	if !ok || len(skills) != 1 || skills[0] != "jianxiong" {
		t.Errorf("bad player3 skills: %v", p3["skills"])
	}
}

func TestBuildQuickStartConfigDefaults(t *testing.T) {
	// 全部留空:默认模式/2人/座位1谋徐盛+座位2士兵,全控默认开
	vals := []string{"", "", "", "", "", ""}
	cfg, err := buildQuickStartConfig(vals)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	players := cfg["players"].(map[string]any)
	if cfg["gameMode"] != "aaa_role_mode" || cfg["playerNum"] != 2 {
		t.Errorf("bad defaults: %v", cfg)
	}
	if players["1"].(map[string]any)["general"] != "mouxusheng" {
		t.Errorf("seat1 should be mouxusheng: %v", players["1"])
	}
	if players["2"].(map[string]any)["general"] != "blank_shibing" {
		t.Errorf("seat2 should be blank_shibing: %v", players["2"])
	}
	if players["1"].(map[string]any)["controlOther"] != true {
		t.Errorf("controlOther should default true: %v", players["1"])
	}

	// 全控显式关闭
	vals[5] = "false"
	cfg, _ = buildQuickStartConfig(vals)
	players = cfg["players"].(map[string]any)
	if _, ok := players["1"].(map[string]any)["controlOther"]; ok {
		t.Errorf("controlOther should be disabled: %v", players["1"])
	}
}

func TestBuildQuickStartConfigSingleGeneral(t *testing.T) {
	// 5人只指定自己:座位2谋徐盛,其余士兵
	vals := []string{"aaa_role_mode", "5", "1:caocao", "", "", "true"}
	cfg, err := buildQuickStartConfig(vals)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	players := cfg["players"].(map[string]any)
	expect := map[string]string{
		"1": "caocao",
		"2": "mouxusheng",
		"3": "blank_shibing",
		"4": "blank_shibing",
		"5": "blank_shibing",
	}
	for seat, want := range expect {
		got := players[seat].(map[string]any)["general"]
		if got != want {
			t.Errorf("seat %s general = %v, want %s", seat, got, want)
		}
	}
}

// 快速启动模式:身份/属性等字段留空也必须能提交(Ctrl+S/Enter)。
func TestFormCanSubmitQuickStart(t *testing.T) {
	m := newModel()
	m.mode = ModeQuickStart
	m.setupQuickStartFields()
	// 全部清空仍可提交(所有字段都有默认行为)
	for _, f := range m.fields {
		f.input.SetValue("")
	}
	if !m.formCanSubmit() {
		t.Error("quick-start form should be submittable with empty fields")
	}
	// 其他模式不受影响:空字段不可提交
	m.mode = ModePortrait
	m.setupQuickStartFields() // 复用字段数量,仅验证 allFilled 分支
	for _, f := range m.fields {
		f.input.SetValue("")
	}
	if m.formCanSubmit() {
		t.Error("portrait form with empty fields should NOT be submittable")
	}
}

// 模式选择左右循环:最左按左回最后一个,最右按右回第一个。
func TestModeSelectWrap(t *testing.T) {
	m := newModel()

	m.mode = ModePortrait
	m2, _ := m.updateModeSelect(tea.KeyMsg{Type: tea.KeyLeft})
	if got := m2.(model).mode; got != ModeQuickStart {
		t.Errorf("left on first should wrap to last, got %v", got)
	}

	m.mode = ModeQuickStart
	m2, _ = m.updateModeSelect(tea.KeyMsg{Type: tea.KeyRight})
	if got := m2.(model).mode; got != ModePortrait {
		t.Errorf("right on last should wrap to first, got %v", got)
	}

	m.mode = ModeDeath
	m2, _ = m.updateModeSelect(tea.KeyMsg{Type: tea.KeyLeft})
	if got := m2.(model).mode; got != ModePortrait {
		t.Errorf("left from second should go to first, got %v", got)
	}
	// 基于上一次返回的模型继续操作(值接收者,需传递返回值)
	m3, _ := m2.(model).updateModeSelect(tea.KeyMsg{Type: tea.KeyRight})
	if got := m3.(model).mode; got != ModeDeath {
		t.Errorf("right from first should go back to second, got %v", got)
	}
}

// 不填武将配置时:1个谋徐盛 + 其余士兵,且确认页摘要能正确渲染。
func TestEmptyGeneralsConfigAndSeatSummary(t *testing.T) {
	vals := []string{"aaa_role_mode", "4", "", "", "", "true"}
	cfg, err := buildQuickStartConfig(vals)
	if err != nil {
		t.Fatalf("empty general config should not error: %v", err)
	}
	players := cfg["players"].(map[string]any)
	expect := map[int]string{1: "mouxusheng", 2: "blank_shibing", 3: "blank_shibing", 4: "blank_shibing"}
	for seat, want := range expect {
		got := players[strconv.Itoa(seat)].(map[string]any)["general"]
		if got != want {
			t.Errorf("seat %d general = %v, want %s", seat, got, want)
		}
	}
	if got := seatSummary(players, 1); got != "mouxusheng (lord, 全控)" {
		t.Errorf("seat1 summary = %q, want %q", got, "mouxusheng (lord, 全控)")
	}
	if got := seatSummary(players, 2); got != "blank_shibing (loyalist)" {
		t.Errorf("seat2 summary = %q", got)
	}
	// 默认身份列表按人数分配：4人局 = lord/loyalist/rebel/renegade
	if got := players["3"].(map[string]any)["role"]; got != "rebel" {
		t.Errorf("seat3 role = %v, want rebel", got)
	}
	if got := players["4"].(map[string]any)["role"]; got != "renegade" {
		t.Errorf("seat4 role = %v, want renegade", got)
	}
}

func TestSummarizeSkillTest(t *testing.T) {
	okOut := "[log]\nRan 1 tests in 0.046 seconds, 1 success, 0 failures\nOK\n"
	got := summarizeSkillTest(okOut)
	if !strings.Contains(got, "Ran 1 tests") || !strings.Contains(got, "OK") {
		t.Errorf("bad success summary: %q", got)
	}

	failOut := "Ran 1 tests in 0.1 seconds, 0 successes, 1 failure\nFailed tests:\n1) Testx.test1\n./file.lua:10: expected: 1, actual: 2\n"
	got = summarizeSkillTest(failOut)
	if !strings.Contains(got, "Failed tests:") || !strings.Contains(got, "expected: 1, actual: 2") {
		t.Errorf("bad fail summary: %q", got)
	}
}

func TestSetupSkillTestFields(t *testing.T) {
	m := newModel()
	m.mode = ModeSkillTest
	m.setupSkillTestFields()
	if len(m.fields) != 3 {
		t.Errorf("should have 3 fields, got %d", len(m.fields))
	}
	if m.stage != StageForm {
		t.Errorf("should be in form stage")
	}
}

func TestFormCanSubmitSkillTest(t *testing.T) {
	m := newModel()
	m.mode = ModeSkillTest
	m.setupSkillTestFields()
	for _, f := range m.fields {
		f.input.SetValue("")
	}
	if m.formCanSubmit() {
		t.Error("empty pkg/id should not submit")
	}
	m.fields[0].input.SetValue("wanyi")
	m.fields[1].input.SetValue("")
	m.fields[2].input.SetValue("wycf_jianhua")
	if !m.formCanSubmit() {
		t.Error("pkg+id should submit with empty subpkg")
	}
}

func TestModeOrder(t *testing.T) {
	// 测试技能应位于技能文件拷贝之后、快速启动之前
	if ModeSkillTest <= ModeSkillFile || ModeSkillTest >= ModeQuickStart {
		t.Error("ModeSkillTest should be between ModeSkillFile and ModeQuickStart")
	}
}

func TestViewTestRunning(t *testing.T) {
	m := newModel()
	m.stage = StageTestRunning
	m.testSkillName = "wycf_jianhua"
	m.testFilePath = "packages/wanyi/pkg/wanyichunfeng/skills/jianhua.lua"
	m.testFrame = 0
	v := m.viewTestRunning()
	if !strings.Contains(v, "运行中") || !strings.Contains(v, "wycf_jianhua") || !strings.Contains(v, "-testfile") {
		t.Errorf("bad running view: %s", v)
	}
}

func TestTestTick(t *testing.T) {
	m := newModel()
	m.stage = StageTestRunning
	m.testFrame = 0
	m2, cmd := m.Update(testTickMsg{})
	if m2.(model).testFrame != 1 {
		t.Error("frame should advance")
	}
	if cmd == nil {
		t.Error("should reschedule tick while running")
	}
	// 不在运行中阶段时,tick 不应推进也不应重新调度
	m.stage = StageForm
	m.testFrame = 0
	m2, cmd = m.Update(testTickMsg{})
	if m2.(model).testFrame != 0 || cmd != nil {
		t.Error("tick should be ignored outside running stage")
	}
}

func TestSkillTestStageTransitions(t *testing.T) {
	m := newModel()
	m.height = 30
	m.stage = StageTestRunning
	// 测试完成 → 进入独立输出查看页,保存完整输出
	m2, _ := m.Update(skillTestMsg{skillName: "x", output: "line1\nRan 1 tests in 0.1 seconds, 1 success, 0 failures\nOK\n"})
	mm := m2.(model)
	if mm.stage != StageTestOutput {
		t.Error("should enter test output page after test")
	}
	if !mm.testOutputOK || !strings.Contains(mm.testOutput, "Ran 1 tests") {
		t.Errorf("bad success output: %q ok=%v", mm.testOutput, mm.testOutputOK)
	}
	// 失败输出
	m2, _ = m.Update(skillTestMsg{skillName: "x", output: "Ran 1 tests in 0.1 seconds, 0 successes, 1 failure\nFailed tests:\n1) Test\n"})
	mm = m2.(model)
	if mm.testOutputOK || !strings.Contains(mm.testOutput, "Failed tests:") {
		t.Errorf("bad fail output: %q ok=%v", mm.testOutput, mm.testOutputOK)
	}
	// 输出页按 Esc/Q 返回表单
	m2, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m2.(model).stage != StageForm {
		t.Error("esc should return to form from output page")
	}
}

func TestTestOutputScrolling(t *testing.T) {
	m := newModel()
	m.height = 20
	m.stage = StageTestOutput
	m.testOutput = ""
	for i := 1; i <= 50; i++ {
		m.testOutput += fmt.Sprintf("line %d\n", i)
	}
	// 向下/向上滚动
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m2.(model).testOutputScroll != 1 {
		t.Error("down should advance scroll")
	}
	m3, _ := m2.(model).Update(tea.KeyMsg{Type: tea.KeyUp})
	if m3.(model).testOutputScroll != 0 {
		t.Error("up should rewind scroll")
	}
	// End 跳转,视图渲染不 panic 且包含行内容
	m4, _ := m3.(model).Update(tea.KeyMsg{Type: tea.KeyEnd})
	if m4.(model).testOutputScroll <= 0 {
		t.Error("end should jump forward")
	}
	v := m4.(model).viewTestOutput()
	if !strings.Contains(v, "测试通过") && !strings.Contains(v, "测试失败") {
		t.Errorf("bad output view: %q", v)
	}
}

func TestStripANSI(t *testing.T) {
	s := "\x1b[31mred\x1b[0m plain"
	got := stripANSI(s)
	if strings.Contains(got, "\x1b[") {
		t.Errorf("ANSI not stripped: %q", got)
	}
}

// 回归:非快速启动模式在确认页按 Enter 后,quickStartLaunch 不应残留为 true,
// 否则 q 退出时会误启动 FreeKill 快速启动。
func TestQuickStartLaunchNotLeaked(t *testing.T) {	m := newModel()
	m.basePath = t.TempDir()
	m.mode = ModePortrait
	// 模拟:任意非快速启动模式走到确认页并按 Enter 执行
	m.setupSkillFileFields()
	m2, _ := m.updateConfirm(tea.KeyMsg{Type: tea.KeyEnter})
	if m2.(model).quickStartLaunch {
		t.Error("quickStartLaunch must not be set for non-quickstart modes")
	}
	// 快速启动模式则应设置
	m.mode = ModeQuickStart
	m.setupQuickStartFields()
	m3, _ := m.updateConfirm(tea.KeyMsg{Type: tea.KeyEnter})
	if !m3.(model).quickStartLaunch {
		t.Error("quickStartLaunch should be set for quickstart mode")
	}
}

// 回归:技能测试进程必须继承真实终端的 stdin/stdout,
// 这样 Lua 文件里的 dbg() 断点才能像在终端里直接运行一样进行输入交互。
func TestRunSkillTestCmdInteractive(t *testing.T) {
	base := t.TempDir()

	// 未找到 FreeKill 可执行文件时应报错
	if _, _, err := runSkillTestCmd(base, "x.lua"); err == nil {
		t.Error("should error when FreeKill binary is missing")
	}

	// 放一个假的 FreeKill 可执行文件
	bin := filepath.Join(base, "FreeKill")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	cmd, buf, err := runSkillTestCmd(base, filepath.Join("packages", "wanyi", "pkg", "wanyifanxing", "skills", "fubuking.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd.Args) != 3 || cmd.Args[1] != "-testfile" {
		t.Errorf("bad command args: %v", cmd.Args)
	}
	if cmd.Dir != base {
		t.Errorf("cmd.Dir = %q, want %q", cmd.Dir, base)
	}

	// stdin 必须保持为 nil(继承终端),dbg() 的 io.read() 才能读到输入
	if cmd.Stdin != nil {
		t.Errorf("stdin must stay inherited (nil) for dbg() interaction, got %T", cmd.Stdin)
	}

	// stdout/stderr 必须 tee 到终端与缓冲区,结果查看页才有输出可显示:
	// 直接写入并检查缓冲区能同步收到内容
	if _, err := cmd.Stdout.Write([]byte("tee-out")); err != nil {
		t.Fatal(err)
	}
	if _, err := cmd.Stderr.Write([]byte("tee-err")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "tee-out") || !strings.Contains(buf.String(), "tee-err") {
		t.Errorf("buffer should capture stdout/stderr, got %q", buf.String())
	}
	if buf == nil {
		t.Error("capture buffer should not be nil")
	}
}
