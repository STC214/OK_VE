//go:build windows

package gui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	appcore "onekeyvego/internal/app"
	"onekeyvego/internal/ffmpeg"

	"github.com/lxn/win"
)

const (
	windowClassName = "OneKeyVEWindow"
	windowTitle     = "OneKeyVE"
)

const (
	idWorkDirEdit = 1001 + iota
	idWorkDirBrowse
	idOutputDirEdit
	idOutputDirBrowse
	idFFmpegEdit
	idFFmpegBrowse
	idFFprobeEdit
	idFFprobeBrowse
	idUseCFFmpeg
	idScan
	idEncoderCombo
	idBlurEdit
	idFeatherEdit
	idRun
	idOpenOutput
	idSummaryEdit
	idStatusStatic
	idLogEdit
	idProgressBar
)

const (
	msgLogUpdate = win.WM_APP + 1
	msgRunDone   = win.WM_APP + 2
)

const (
	iconSmall                    = 0
	iconBig                      = 1
	dwmwaUseImmersiveDarkMode    = 20
	dwmwaUseImmersiveDarkModeOld = 19
	odtButton                    = 4
	bifReturnOnlyFSDirs          = 0x0001
	bifEditBox                   = 0x0010
	bifNewDialogStyle            = 0x0040
	bffmInitialized              = 1
	bffmSetSelectionW            = win.WM_USER + 103
)

var (
	dwmapi                    = syscall.NewLazyDLL("dwmapi.dll")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
)

type uiState struct {
	hwnd win.HWND

	workDirEdit   win.HWND
	outputDirEdit win.HWND
	ffmpegEdit    win.HWND
	ffprobeEdit   win.HWND
	encoderCombo  win.HWND
	blurEdit      win.HWND
	featherEdit   win.HWND
	summaryEdit   win.HWND
	statusStatic  win.HWND
	logEdit       win.HWND
	progressBar   win.HWND
	runButton     win.HWND

	fontNormal win.HFONT
	fontTitle  win.HFONT

	bgBrush             win.HBRUSH
	panelBrush          win.HBRUSH
	headerBrush         win.HBRUSH
	editBrush           win.HBRUSH
	accentBrush         win.HBRUSH
	buttonBrush         win.HBRUSH
	buttonPrimaryBrush  win.HBRUSH
	buttonDisabledBrush win.HBRUSH

	mu               sync.Mutex
	logLines         []string
	statusText       string
	summary          string
	progress         int
	running          bool
	runErr           error
	refreshPending   bool
	renderedLog      string
	renderedStatus   string
	renderedProgress int
}

var globalState *uiState

func Run() error {
	runtime.LockOSThread()

	hr := win.OleInitialize()
	if hr != win.S_OK && hr != win.S_FALSE {
		return fmt.Errorf("ole initialize failed: 0x%x", uint32(hr))
	}
	defer win.OleUninitialize()

	instance := win.GetModuleHandle(nil)
	if instance == 0 {
		return fmt.Errorf("get module handle failed")
	}

	var controls win.INITCOMMONCONTROLSEX
	controls.DwSize = uint32(unsafe.Sizeof(controls))
	controls.DwICC = win.ICC_PROGRESS_CLASS
	win.InitCommonControlsEx(&controls)

	if err := registerClass(instance); err != nil {
		return err
	}

	state := newUIState()
	globalState = state

	hwnd := win.CreateWindowEx(
		0,
		syscall.StringToUTF16Ptr(windowClassName),
		syscall.StringToUTF16Ptr(windowTitle),
		win.WS_OVERLAPPED|win.WS_CAPTION|win.WS_SYSMENU|win.WS_MINIMIZEBOX,
		win.CW_USEDEFAULT,
		win.CW_USEDEFAULT,
		1320,
		900,
		0,
		0,
		instance,
		nil,
	)
	if hwnd == 0 {
		return fmt.Errorf("create window failed")
	}

	state.hwnd = hwnd
	icon := loadAppIcon(instance)
	if icon != 0 {
		win.SendMessage(hwnd, win.WM_SETICON, iconBig, uintptr(icon))
		win.SendMessage(hwnd, win.WM_SETICON, iconSmall, uintptr(icon))
	}
	enableDarkTitleBar(hwnd)

	win.ShowWindow(hwnd, win.SW_SHOW)
	win.UpdateWindow(hwnd)
	centerWindow(hwnd)
	state.updateSummary()

	var msg win.MSG
	for {
		ret := win.GetMessage(&msg, 0, 0, 0)
		if ret == 0 {
			break
		}
		if ret == -1 {
			return fmt.Errorf("message loop failed")
		}
		win.TranslateMessage(&msg)
		win.DispatchMessage(&msg)
	}

	return nil
}

func newUIState() *uiState {
	return &uiState{
		bgBrush:             createSolidBrush(win.RGB(14, 17, 22)),
		panelBrush:          createSolidBrush(win.RGB(19, 23, 31)),
		headerBrush:         createSolidBrush(win.RGB(16, 19, 26)),
		editBrush:           createSolidBrush(win.RGB(24, 29, 38)),
		accentBrush:         createSolidBrush(win.RGB(74, 88, 104)),
		buttonBrush:         createSolidBrush(win.RGB(34, 40, 52)),
		buttonPrimaryBrush:  createSolidBrush(win.RGB(62, 78, 96)),
		buttonDisabledBrush: createSolidBrush(win.RGB(43, 47, 55)),
		statusText:          "\u51c6\u5907\u5c31\u7eea",
	}
}

func registerClass(instance win.HINSTANCE) error {
	var wc win.WNDCLASSEX
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	wc.LpfnWndProc = syscall.NewCallback(wndProc)
	wc.HInstance = instance
	wc.HCursor = win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_ARROW))
	wc.HIcon = loadAppIcon(instance)
	wc.HIconSm = wc.HIcon
	wc.HbrBackground = win.COLOR_WINDOW + 1
	wc.LpszClassName = syscall.StringToUTF16Ptr(windowClassName)
	if win.RegisterClassEx(&wc) == 0 {
		return fmt.Errorf("register window class failed")
	}
	return nil
}

func wndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	state := globalState

	switch msg {
	case win.WM_CREATE:
		if state != nil {
			state.hwnd = hwnd
			state.initControls()
		}
		return 0
	case win.WM_COMMAND:
		if state != nil {
			state.handleCommand(win.LOWORD(uint32(wParam)))
		}
		return 0
	case win.WM_DRAWITEM:
		if state != nil {
			return state.drawButton((*win.DRAWITEMSTRUCT)(unsafe.Pointer(lParam)))
		}
		return 0
	case msgLogUpdate:
		if state != nil {
			state.refreshLog()
		}
		return 0
	case msgRunDone:
		if state != nil {
			state.finishRun()
		}
		return 0
	case win.WM_CTLCOLORSTATIC:
		if state != nil {
			hdc := win.HDC(wParam)
			win.SetBkMode(hdc, win.TRANSPARENT)
			win.SetTextColor(hdc, win.RGB(226, 232, 239))
			return uintptr(state.panelBrush)
		}
	case win.WM_CTLCOLOREDIT:
		if state != nil {
			hdc := win.HDC(wParam)
			win.SetBkColor(hdc, win.RGB(24, 29, 38))
			win.SetTextColor(hdc, win.RGB(236, 239, 244))
			return uintptr(state.editBrush)
		}
	case win.WM_ERASEBKGND:
		if state != nil {
			state.paintBackground(win.HDC(wParam))
			return 1
		}
	case win.WM_PAINT:
		if state != nil {
			state.paintWindow()
			return 0
		}
	case win.WM_CLOSE:
		if state != nil && state.isRunning() {
			messageBox(hwnd, "\u6b63\u5728\u5904\u7406", "\u5f53\u524d\u4ecd\u6709\u4efb\u52a1\u5728\u8fd0\u884c\uff0c\u8bf7\u7b49\u5f85\u5904\u7406\u5b8c\u6210\u540e\u518d\u5173\u95ed\u7a97\u53e3\u3002", win.MB_ICONINFORMATION)
			return 0
		}
		win.DestroyWindow(hwnd)
		return 0
	case win.WM_DESTROY:
		win.PostQuitMessage(0)
		return 0
	}

	return win.DefWindowProc(hwnd, msg, wParam, lParam)
}

func (s *uiState) initControls() {
	s.fontNormal = createFont(18, 400, "Segoe UI")
	s.fontTitle = createFont(32, 700, "Segoe UI Semibold")
	workDir := preferredWorkDir(currentWorkingDir())
	outputDir := filepath.Join(workDir, "output-gui")
	createLabel(s.hwnd, "\u89c6\u9891\u76ee\u5f55", 28, 118, 140, 24, s.fontNormal)
	s.workDirEdit = createEdit(s.hwnd, idWorkDirEdit, workDir, 28, 146, 360, 30)
	createButton(s.hwnd, idWorkDirBrowse, "\u6d4f\u89c8\u76ee\u5f55", 398, 146, 104, 30)
	createLabel(s.hwnd, "\u8f93\u51fa\u76ee\u5f55", 28, 190, 140, 24, s.fontNormal)
	s.outputDirEdit = createEdit(s.hwnd, idOutputDirEdit, outputDir, 28, 218, 360, 30)
	createButton(s.hwnd, idOutputDirBrowse, "\u6d4f\u89c8\u76ee\u5f55", 398, 218, 104, 30)
	createLabel(s.hwnd, "FFmpeg", 28, 262, 100, 24, s.fontNormal)
	s.ffmpegEdit = createEdit(s.hwnd, idFFmpegEdit, firstNonEmpty(os.Getenv("ONEKEYVE_FFMPEG"), os.Getenv("FFMPEG_PATH"), "C:\\ffmpeg\\bin\\ffmpeg.exe"), 28, 290, 360, 30)
	createButton(s.hwnd, idFFmpegBrowse, "\u9009\u62e9\u6587\u4ef6", 398, 290, 104, 30)
	createLabel(s.hwnd, "FFprobe", 28, 334, 100, 24, s.fontNormal)
	s.ffprobeEdit = createEdit(s.hwnd, idFFprobeEdit, firstNonEmpty(os.Getenv("ONEKEYVE_FFPROBE"), os.Getenv("FFPROBE_PATH"), "C:\\ffmpeg\\bin\\ffprobe.exe"), 28, 362, 360, 30)
	createButton(s.hwnd, idFFprobeBrowse, "\u9009\u62e9\u6587\u4ef6", 398, 362, 104, 30)
	createButton(s.hwnd, idUseCFFmpeg, "\u4f7f\u7528 C:\\ffmpeg", 28, 406, 160, 32)
	createButton(s.hwnd, idScan, "\u626b\u63cf\u73af\u5883", 198, 406, 120, 32)
	createLabel(s.hwnd, "\u7f16\u7801\u5668", 28, 462, 100, 24, s.fontNormal)
	s.encoderCombo = createComboBox(s.hwnd, idEncoderCombo, 28, 490, 220, 240)
	addComboItems(s.encoderCombo, []string{"\u81ea\u52a8", "h264_nvenc", "libx264"})
	win.SendMessage(s.encoderCombo, win.CB_SETCURSEL, 0, 0)
	createLabel(s.hwnd, "\u80cc\u666f\u6a21\u7cca", 272, 462, 120, 24, s.fontNormal)
	s.blurEdit = createEdit(s.hwnd, idBlurEdit, "20", 272, 490, 90, 30)
	createLabel(s.hwnd, "\u7fbd\u5316\u50cf\u7d20", 388, 462, 120, 24, s.fontNormal)
	s.featherEdit = createEdit(s.hwnd, idFeatherEdit, "30", 388, 490, 90, 30)
	s.runButton = createButton(s.hwnd, idRun, "\u5f00\u59cb\u5904\u7406", 28, 548, 220, 42)
	createButton(s.hwnd, idOpenOutput, "\u6253\u5f00\u8f93\u51fa\u76ee\u5f55", 262, 548, 160, 42)
	createLabel(s.hwnd, "\u8fd0\u884c\u72b6\u6001", 28, 612, 100, 24, s.fontNormal)
	s.statusStatic = createLabel(s.hwnd, s.statusText, 28, 640, 474, 48, s.fontNormal)
	s.progressBar = createProgressBar(s.hwnd, idProgressBar, 28, 698, 474, 24)
	win.SendMessage(s.progressBar, win.PBM_SETRANGE32, 0, 1000)
	win.ShowWindow(s.progressBar, win.SW_HIDE)
	createLabel(s.hwnd, "\u73af\u5883\u6458\u8981", 540, 118, 180, 24, s.fontNormal)
	s.summaryEdit = createReadOnlyEdit(s.hwnd, idSummaryEdit, "", 540, 146, 736, 176)
	createLabel(s.hwnd, "\u8fd0\u884c\u65e5\u5fd7", 540, 350, 100, 24, s.fontNormal)
	s.logEdit = createReadOnlyEdit(s.hwnd, idLogEdit, "", 540, 378, 736, 424)
}
func (s *uiState) handleCommand(id uint16) {
	switch int(id) {
	case idWorkDirBrowse:
		if selected, ok := chooseFolder(getWindowText(s.workDirEdit)); ok {
			setWindowText(s.workDirEdit, selected)
			if strings.TrimSpace(getWindowText(s.outputDirEdit)) == "" {
				setWindowText(s.outputDirEdit, filepath.Join(selected, "output-gui"))
			}
			s.updateSummary()
		}
	case idOutputDirBrowse:
		if selected, ok := chooseFolder(getWindowText(s.outputDirEdit)); ok {
			setWindowText(s.outputDirEdit, selected)
			s.updateSummary()
		}
	case idFFmpegBrowse:
		if selected, ok := chooseFile(getWindowText(s.ffmpegEdit), "ffmpeg.exe|ffmpeg.exe|\u6240\u6709\u6587\u4ef6|*.*"); ok {
			setWindowText(s.ffmpegEdit, selected)
			s.updateSummary()
		}
	case idFFprobeBrowse:
		if selected, ok := chooseFile(getWindowText(s.ffprobeEdit), "ffprobe.exe|ffprobe.exe|\u6240\u6709\u6587\u4ef6|*.*"); ok {
			setWindowText(s.ffprobeEdit, selected)
			s.updateSummary()
		}
	case idUseCFFmpeg:
		setWindowText(s.ffmpegEdit, "C:\\ffmpeg\\bin\\ffmpeg.exe")
		setWindowText(s.ffprobeEdit, "C:\\ffmpeg\\bin\\ffprobe.exe")
		s.updateSummary()
	case idScan:
		s.updateSummary()
		messageBox(s.hwnd, "\u626b\u63cf\u5b8c\u6210", s.summary, win.MB_ICONINFORMATION)
	case idOpenOutput:
		outputDir := strings.TrimSpace(getWindowText(s.outputDirEdit))
		if outputDir == "" {
			messageBox(s.hwnd, "\u63d0\u793a", "\u8f93\u51fa\u76ee\u5f55\u4e0d\u80fd\u4e3a\u7a7a\u3002", win.MB_ICONINFORMATION)
			return
		}
		if err := os.MkdirAll(outputDir, 0o755); err != nil {
			messageBox(s.hwnd, "\u9519\u8bef", err.Error(), win.MB_ICONERROR)
			return
		}
		win.ShellExecute(
			s.hwnd,
			syscall.StringToUTF16Ptr("open"),
			syscall.StringToUTF16Ptr(outputDir),
			nil,
			nil,
			win.SW_SHOWNORMAL,
		)
	case idRun:
		s.startRun()
	}
}

func (s *uiState) startRun() {
	if s.isRunning() {
		return
	}
	cfg, err := s.readConfig()
	if err != nil {
		messageBox(s.hwnd, "\u914d\u7f6e\u9519\u8bef", err.Error(), win.MB_ICONERROR)
		return
	}
	cfg.OnLog = s.appendLog
	cfg.OnProgress = s.applyProgress
	s.mu.Lock()
	s.running = true
	s.runErr = nil
	s.statusText = "\u5f00\u59cb\u5904\u7406"
	s.progress = 0
	s.logLines = nil
	s.refreshPending = false
	s.mu.Unlock()
	win.EnableWindow(s.runButton, false)
	win.ShowWindow(s.progressBar, win.SW_SHOW)
	win.SendMessage(s.progressBar, win.PBM_SETPOS, 0, 0)
	s.refreshLog()
	go func() {
		s.appendLog("\u5f00\u59cb\u6267\u884c\u56fe\u5f62\u754c\u9762\u4efb\u52a1\u3002")
		s.appendLog("\u5de5\u4f5c\u76ee\u5f55: " + cfg.WorkDir)
		s.appendLog("\u8f93\u51fa\u76ee\u5f55: " + cfg.OutputDir)
		err := appcore.Run(cfg)
		s.mu.Lock()
		s.running = false
		s.runErr = err
		if err != nil {
			s.statusText = "\u5904\u7406\u5931\u8d25"
			s.progress = 0
		} else {
			s.statusText = "\u5904\u7406\u5b8c\u6210"
			s.progress = 1000
			s.prependLogLocked("\u5168\u90e8\u5904\u7406\u5b8c\u6210\u3002")
		}
		s.requestRefreshLocked()
		s.mu.Unlock()
		win.PostMessage(s.hwnd, msgRunDone, 0, 0)
	}()
}
func (s *uiState) finishRun() {
	s.refreshLog()
	win.EnableWindow(s.runButton, true)
	if s.runErr != nil {
		win.ShowWindow(s.progressBar, win.SW_HIDE)
		messageBox(s.hwnd, "\u5904\u7406\u5931\u8d25", s.runErr.Error(), win.MB_ICONERROR)
		return
	}
	win.SendMessage(s.progressBar, win.PBM_SETPOS, 1000, 0)
	messageBox(s.hwnd, "\u5b8c\u6210", "\u6240\u6709\u4efb\u52a1\u5df2\u7ecf\u5904\u7406\u5b8c\u6210\u3002", win.MB_ICONINFORMATION)
}
func (s *uiState) readConfig() (appcore.Config, error) {
	workDir := strings.TrimSpace(getWindowText(s.workDirEdit))
	outputDir := strings.TrimSpace(getWindowText(s.outputDirEdit))
	ffmpegPath := strings.TrimSpace(getWindowText(s.ffmpegEdit))
	ffprobePath := strings.TrimSpace(getWindowText(s.ffprobeEdit))
	if workDir == "" {
		return appcore.Config{}, fmt.Errorf("\u89c6\u9891\u76ee\u5f55\u4e0d\u80fd\u4e3a\u7a7a")
	}
	if outputDir == "" {
		return appcore.Config{}, fmt.Errorf("\u8f93\u51fa\u76ee\u5f55\u4e0d\u80fd\u4e3a\u7a7a")
	}
	cfg := appcore.DefaultConfig(workDir)
	cfg.WorkDir = workDir
	cfg.OutputDir = outputDir
	cfg.FFmpegPath = ffmpegPath
	cfg.FFprobePath = ffprobePath
	blur, err := strconv.Atoi(strings.TrimSpace(getWindowText(s.blurEdit)))
	if err != nil {
		return cfg, fmt.Errorf("\u80cc\u666f\u6a21\u7cca\u53c2\u6570\u5fc5\u987b\u662f\u6574\u6570")
	}
	feather, err := strconv.Atoi(strings.TrimSpace(getWindowText(s.featherEdit)))
	if err != nil {
		return cfg, fmt.Errorf("\u7fbd\u5316\u50cf\u7d20\u53c2\u6570\u5fc5\u987b\u662f\u6574\u6570")
	}
	cfg.BlurSigma = blur
	cfg.FeatherPx = feather
	encoder := getComboSelection(s.encoderCombo)
	if encoder != "\u81ea\u52a8" {
		cfg.Encoder = encoder
	}
	return cfg, nil
}
func (s *uiState) updateSummary() {
	workDir := strings.TrimSpace(getWindowText(s.workDirEdit))
	outputDir := strings.TrimSpace(getWindowText(s.outputDirEdit))
	videos, videoErr := ffmpeg.FindVideos(workDir)
	bins, binErr := ffmpeg.Locate(workDir, ffmpeg.Binaries{
		FFmpeg:  strings.TrimSpace(getWindowText(s.ffmpegEdit)),
		FFprobe: strings.TrimSpace(getWindowText(s.ffprobeEdit)),
	})
	lines := []string{}
	if videoErr != nil {
		lines = append(lines, "\u89c6\u9891\u626b\u63cf\u5931\u8d25: "+videoErr.Error())
	} else {
		lines = append(lines, fmt.Sprintf("\u68c0\u6d4b\u5230 %d \u4e2a\u89c6\u9891\u6587\u4ef6", len(videos)))
	}
	if binErr != nil {
		lines = append(lines, "\u7ec4\u4ef6\u5b9a\u4f4d\u5931\u8d25: "+binErr.Error())
	} else {
		lines = append(lines, "FFmpeg: "+bins.FFmpeg)
		lines = append(lines, "FFprobe: "+bins.FFprobe)
		lines = append(lines, "\u6765\u6e90: "+bins.Source)
	}
	lines = append(lines, "\u8f93\u51fa\u76ee\u5f55: "+outputDir)
	lines = append(lines, "\u8bf4\u660e: \u5141\u8bb8\u8bfb\u53d6 C:\\ffmpeg\uff0c\u4f46\u4e0d\u4f1a\u628a\u8f93\u51fa\u5199\u5165 C \u76d8\u3002")
	s.mu.Lock()
	s.summary = strings.Join(lines, "\r\n")
	s.mu.Unlock()
	setWindowText(s.summaryEdit, s.summary)
}
func (s *uiState) prependLogLocked(line string) {
	if line == "" {
		return
	}
	if len(s.logLines) == 0 {
		s.logLines = append(s.logLines, line)
	} else {
		s.logLines = append(s.logLines, "")
		copy(s.logLines[1:], s.logLines[:len(s.logLines)-1])
		s.logLines[0] = line
	}
	if len(s.logLines) > 500 {
		s.logLines = s.logLines[:500]
	}
}
func (s *uiState) requestRefreshLocked() {
	if s.refreshPending || s.hwnd == 0 {
		return
	}
	s.refreshPending = true
	win.PostMessage(s.hwnd, msgLogUpdate, 0, 0)
}
func (s *uiState) applyProgress(update appcore.ProgressUpdate) {
	progress := int(update.Percent * 10)
	if progress < 0 {
		progress = 0
	}
	if progress > 1000 {
		progress = 1000
	}
	currentTask := update.CurrentTask
	if currentTask == 0 && update.CompletedTasks > 0 {
		currentTask = update.CompletedTasks
	}
	if currentTask == 0 {
		currentTask = 1
	}
	status := "\u5f00\u59cb\u5904\u7406"
	if update.TotalTasks > 0 {
		status = fmt.Sprintf("\u6b63\u5728\u5904\u7406 %d/%d", currentTask, update.TotalTasks)
		if update.VideoName != "" {
			status += ": " + update.VideoName
		}
		if update.TargetLabel != "" {
			status += " [" + update.TargetLabel + "]"
		}
		if update.TotalFrames > 0 && update.CurrentFrame > 0 {
			status += fmt.Sprintf(" %d/%d", update.CurrentFrame, update.TotalFrames)
		}
		status += fmt.Sprintf(" %.1f%%", update.Percent)
	}
	s.mu.Lock()
	if progress == s.progress && status == s.statusText {
		s.mu.Unlock()
		return
	}
	s.progress = progress
	s.statusText = status
	s.requestRefreshLocked()
	s.mu.Unlock()
}
func (s *uiState) appendLog(line string) {
	line = strings.TrimSpace(strings.ReplaceAll(line, "\r\n", "\n"))
	if line == "" {
		return
	}
	s.mu.Lock()
	for _, chunk := range strings.Split(line, "\n") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		s.prependLogLocked(chunk)
	}
	s.requestRefreshLocked()
	s.mu.Unlock()
}
func (s *uiState) refreshLog() {
	s.mu.Lock()
	s.refreshPending = false
	logText := strings.Join(s.logLines, "\r\n")
	statusText := s.statusText
	progress := s.progress
	s.mu.Unlock()
	if logText != s.renderedLog {
		setWindowText(s.logEdit, logText)
		sendToTop(s.logEdit)
		s.renderedLog = logText
	}
	if statusText != s.renderedStatus {
		setWindowText(s.statusStatic, statusText)
		s.renderedStatus = statusText
	}
	if progress != s.renderedProgress {
		win.SendMessage(s.progressBar, win.PBM_SETPOS, uintptr(progress), 0)
		s.renderedProgress = progress
	}
}
func (s *uiState) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *uiState) paintBackground(hdc win.HDC) {
	var rect win.RECT
	win.GetClientRect(s.hwnd, &rect)
	fillRect(hdc, s.bgBrush, rect.Left, rect.Top, rect.Right, rect.Bottom)
}

func (s *uiState) paintWindow() {
	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(s.hwnd, &ps)
	defer win.EndPaint(s.hwnd, &ps)
	var rect win.RECT
	win.GetClientRect(s.hwnd, &rect)
	fillRect(hdc, s.bgBrush, rect.Left, rect.Top, rect.Right, rect.Bottom)
	fillRect(hdc, s.headerBrush, 0, 0, rect.Right, 92)
	fillRect(hdc, s.accentBrush, 0, 88, rect.Right, 92)
	oldFont := win.SelectObject(hdc, win.HGDIOBJ(s.fontTitle))
	oldColor := win.SetTextColor(hdc, win.RGB(245, 247, 250))
	oldMode := win.SetBkMode(hdc, win.TRANSPARENT)
	titleRect := win.RECT{Left: 28, Top: 18, Right: 600, Bottom: 62}
	drawText(hdc, "OneKeyVE", &titleRect, win.DT_LEFT|win.DT_VCENTER|win.DT_SINGLELINE)
	win.SelectObject(hdc, oldFont)
	win.SelectObject(hdc, win.HGDIOBJ(s.fontNormal))
	win.SetTextColor(hdc, win.RGB(170, 180, 194))
	subRect := win.RECT{Left: 30, Top: 56, Right: 560, Bottom: 80}
	drawText(hdc, "\u89c6\u9891\u6279\u5904\u7406\u684c\u9762\u7248", &subRect, win.DT_LEFT|win.DT_VCENTER|win.DT_SINGLELINE)
	win.SetTextColor(hdc, win.RGB(203, 210, 220))
	rightRect := win.RECT{Left: rect.Right - 420, Top: 38, Right: rect.Right - 30, Bottom: 70}
	drawText(hdc, "\u6df1\u8272\u754c\u9762 \u00b7 \u5b9e\u65f6\u65e5\u5fd7 \u00b7 \u76ee\u5f55\u76f4\u8fbe", &rightRect, win.DT_RIGHT|win.DT_VCENTER|win.DT_SINGLELINE)
	win.SetTextColor(hdc, oldColor)
	win.SetBkMode(hdc, oldMode)
}

func (s *uiState) drawButton(dis *win.DRAWITEMSTRUCT) uintptr {
	if dis == nil || dis.CtlType != odtButton {
		return 0
	}

	rect := dis.RcItem
	brush := s.buttonBrush
	textColor := win.RGB(226, 232, 239)
	borderColor := win.RGB(70, 80, 94)

	if dis.CtlID == idRun {
		brush = s.buttonPrimaryBrush
		textColor = win.RGB(246, 248, 250)
		borderColor = win.RGB(96, 110, 126)
	}
	if dis.ItemState&win.ODS_DISABLED != 0 {
		brush = s.buttonDisabledBrush
		textColor = win.RGB(152, 160, 171)
		borderColor = win.RGB(58, 64, 74)
	}

	fillRect(dis.HDC, brush, rect.Left, rect.Top, rect.Right, rect.Bottom)
	drawRectBorder(dis.HDC, rect, borderColor)

	if dis.ItemState&win.ODS_SELECTED != 0 {
		pressedRect := rect
		pressedRect.Left++
		pressedRect.Top++
		pressedRect.Right--
		pressedRect.Bottom--
		drawRectBorder(dis.HDC, pressedRect, win.RGB(24, 28, 35))
	}

	oldMode := win.SetBkMode(dis.HDC, win.TRANSPARENT)
	oldColor := win.SetTextColor(dis.HDC, textColor)
	textRect := rect
	if dis.ItemState&win.ODS_SELECTED != 0 {
		textRect.Left++
		textRect.Top++
	}
	drawText(dis.HDC, getWindowText(dis.HwndItem), &textRect, win.DT_CENTER|win.DT_VCENTER|win.DT_SINGLELINE)
	win.SetTextColor(dis.HDC, oldColor)
	win.SetBkMode(dis.HDC, oldMode)

	if dis.ItemState&win.ODS_FOCUS != 0 {
		focusRect := rect
		focusRect.Left += 4
		focusRect.Top += 4
		focusRect.Right -= 4
		focusRect.Bottom -= 4
		win.DrawFocusRect(dis.HDC, &focusRect)
	}

	return 1
}

func createLabel(parent win.HWND, text string, x, y, w, h int32, font win.HFONT) win.HWND {
	hwnd := win.CreateWindowEx(
		0,
		syscall.StringToUTF16Ptr("STATIC"),
		syscall.StringToUTF16Ptr(text),
		win.WS_CHILD|win.WS_VISIBLE,
		x, y, w, h,
		parent,
		0,
		0,
		nil,
	)
	applyFont(hwnd, font)
	return hwnd
}

func createButton(parent win.HWND, id int, text string, x, y, w, h int32) win.HWND {
	hwnd := win.CreateWindowEx(
		0,
		syscall.StringToUTF16Ptr("BUTTON"),
		syscall.StringToUTF16Ptr(text),
		win.WS_CHILD|win.WS_VISIBLE|win.WS_TABSTOP|win.BS_OWNERDRAW,
		x, y, w, h,
		parent,
		win.HMENU(id),
		0,
		nil,
	)
	applyFont(hwnd, globalState.fontNormal)
	return hwnd
}

func createEdit(parent win.HWND, id int, text string, x, y, w, h int32) win.HWND {
	hwnd := win.CreateWindowEx(
		win.WS_EX_CLIENTEDGE,
		syscall.StringToUTF16Ptr("EDIT"),
		syscall.StringToUTF16Ptr(text),
		win.WS_CHILD|win.WS_VISIBLE|win.WS_TABSTOP|win.ES_AUTOHSCROLL,
		x, y, w, h,
		parent,
		win.HMENU(id),
		0,
		nil,
	)
	applyFont(hwnd, globalState.fontNormal)
	return hwnd
}

func createReadOnlyEdit(parent win.HWND, id int, text string, x, y, w, h int32) win.HWND {
	hwnd := win.CreateWindowEx(
		win.WS_EX_CLIENTEDGE,
		syscall.StringToUTF16Ptr("EDIT"),
		syscall.StringToUTF16Ptr(text),
		win.WS_CHILD|win.WS_VISIBLE|win.WS_VSCROLL|win.ES_MULTILINE|win.ES_AUTOVSCROLL|win.ES_READONLY,
		x, y, w, h,
		parent,
		win.HMENU(id),
		0,
		nil,
	)
	applyFont(hwnd, globalState.fontNormal)
	return hwnd
}

func createComboBox(parent win.HWND, id int, x, y, w, h int32) win.HWND {
	hwnd := win.CreateWindowEx(
		0,
		syscall.StringToUTF16Ptr("COMBOBOX"),
		nil,
		win.WS_CHILD|win.WS_VISIBLE|win.WS_TABSTOP|win.CBS_DROPDOWNLIST|win.WS_VSCROLL,
		x, y, w, h,
		parent,
		win.HMENU(id),
		0,
		nil,
	)
	applyFont(hwnd, globalState.fontNormal)
	return hwnd
}

func createProgressBar(parent win.HWND, id int, x, y, w, h int32) win.HWND {
	return win.CreateWindowEx(
		0,
		syscall.StringToUTF16Ptr("msctls_progress32"),
		nil,
		win.WS_CHILD|win.WS_VISIBLE,
		x, y, w, h,
		parent,
		win.HMENU(id),
		0,
		nil,
	)
}

func addComboItems(hwnd win.HWND, items []string) {
	for _, item := range items {
		win.SendMessage(hwnd, win.CB_ADDSTRING, 0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(item))))
	}
}

func getComboSelection(hwnd win.HWND) string {
	index := int(win.SendMessage(hwnd, win.CB_GETCURSEL, 0, 0))
	if index < 0 {
		return ""
	}
	buf := make([]uint16, 256)
	win.SendMessage(hwnd, win.CB_GETLBTEXT, uintptr(index), uintptr(unsafe.Pointer(&buf[0])))
	return syscall.UTF16ToString(buf)
}

func createFont(height int32, weight int32, face string) win.HFONT {
	var font win.LOGFONT
	font.LfHeight = height
	font.LfWeight = weight
	font.LfCharSet = win.DEFAULT_CHARSET
	font.LfOutPrecision = win.OUT_OUTLINE_PRECIS
	font.LfClipPrecision = win.CLIP_DEFAULT_PRECIS
	font.LfQuality = win.CLEARTYPE_QUALITY
	font.LfPitchAndFamily = win.DEFAULT_PITCH | win.FF_DONTCARE
	copy(font.LfFaceName[:], syscall.StringToUTF16(face))
	return win.CreateFontIndirect(&font)
}

func applyFont(hwnd win.HWND, font win.HFONT) {
	if hwnd != 0 && font != 0 {
		win.SendMessage(hwnd, win.WM_SETFONT, uintptr(font), 1)
	}
}

func getWindowText(hwnd win.HWND) string {
	length := int32(win.SendMessage(hwnd, win.WM_GETTEXTLENGTH, 0, 0))
	buf := make([]uint16, length+1)
	win.SendMessage(hwnd, win.WM_GETTEXT, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	return syscall.UTF16ToString(buf)
}

func setWindowText(hwnd win.HWND, text string) {
	win.SendMessage(hwnd, win.WM_SETTEXT, 0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(text))))
}

func sendToTop(hwnd win.HWND) {
	win.SendMessage(hwnd, win.EM_SETSEL, 0, 0)
	win.SendMessage(hwnd, win.EM_SCROLLCARET, 0, 0)
}

func chooseFolder(initial string) (string, bool) {
	displayName := make([]uint16, win.MAX_PATH)
	title := syscall.StringToUTF16Ptr("\u9009\u62e9\u76ee\u5f55")
	var initialPtr *uint16
	if strings.TrimSpace(initial) != "" {
		initialPtr = syscall.StringToUTF16Ptr(initial)
	}
	callback := syscall.NewCallback(func(hwnd uintptr, msg uint32, lParam, data uintptr) uintptr {
		if msg == bffmInitialized && data != 0 {
			win.SendMessage(win.HWND(hwnd), bffmSetSelectionW, 1, data)
		}
		return 0
	})
	bi := win.BROWSEINFO{
		HwndOwner:      globalState.hwnd,
		PszDisplayName: &displayName[0],
		LpszTitle:      title,
		UlFlags:        bifReturnOnlyFSDirs | bifEditBox | bifNewDialogStyle,
		Lpfn:           callback,
		LParam:         uintptr(unsafe.Pointer(initialPtr)),
	}
	pidl := win.SHBrowseForFolder(&bi)
	if pidl == 0 {
		return "", false
	}
	defer win.CoTaskMemFree(pidl)
	pathBuf := make([]uint16, win.MAX_PATH)
	if !win.SHGetPathFromIDList(pidl, &pathBuf[0]) {
		return "", false
	}
	selected := syscall.UTF16ToString(pathBuf)
	if strings.TrimSpace(selected) == "" {
		return "", false
	}
	return selected, true
}

func chooseFile(initial string, filter string) (string, bool) {
	fileBuf := make([]uint16, win.MAX_PATH)
	initialFile := strings.TrimSpace(initial)
	if initialFile != "" {
		copy(fileBuf, syscall.StringToUTF16(initialFile))
	}
	filterText := strings.ReplaceAll(filter, "|", "\x00") + "\x00"
	filterBuf := syscall.StringToUTF16(filterText)
	initialDir := strings.TrimSpace(filepath.Dir(initialFile))
	var initialDirPtr *uint16
	if initialDir != "" && initialDir != "." {
		initialDirPtr = syscall.StringToUTF16Ptr(initialDir)
	}
	ofn := win.OPENFILENAME{
		LStructSize:     uint32(unsafe.Sizeof(win.OPENFILENAME{})),
		HwndOwner:       globalState.hwnd,
		LpstrFilter:     &filterBuf[0],
		LpstrFile:       &fileBuf[0],
		NMaxFile:        uint32(len(fileBuf)),
		LpstrInitialDir: initialDirPtr,
		LpstrTitle:      syscall.StringToUTF16Ptr("\u9009\u62e9\u6587\u4ef6"),
		Flags:           win.OFN_EXPLORER | win.OFN_FILEMUSTEXIST | win.OFN_PATHMUSTEXIST | win.OFN_HIDEREADONLY | win.OFN_NOCHANGEDIR,
	}
	if !win.GetOpenFileName(&ofn) {
		return "", false
	}
	selected := syscall.UTF16ToString(fileBuf)
	if strings.TrimSpace(selected) == "" {
		return "", false
	}
	return selected, true
}

func currentWorkingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

func preferredWorkDir(cwd string) string {
	testDir := filepath.Join(filepath.Dir(cwd), "test")
	if info, err := os.Stat(testDir); err == nil && info.IsDir() {
		return testDir
	}
	return cwd
}

func messageBox(hwnd win.HWND, title string, text string, flags uint32) {
	win.MessageBox(hwnd, syscall.StringToUTF16Ptr(text), syscall.StringToUTF16Ptr(title), flags)
}

func centerWindow(hwnd win.HWND) {
	var rect win.RECT
	win.GetWindowRect(hwnd, &rect)
	width := rect.Right - rect.Left
	height := rect.Bottom - rect.Top
	screenW := win.GetSystemMetrics(win.SM_CXSCREEN)
	screenH := win.GetSystemMetrics(win.SM_CYSCREEN)
	x := (screenW - width) / 2
	y := (screenH - height) / 2
	win.SetWindowPos(hwnd, 0, x, y, 0, 0, win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_NOACTIVATE)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func createSolidBrush(rgb win.COLORREF) win.HBRUSH {
	return win.CreateBrushIndirect(&win.LOGBRUSH{
		LbStyle: win.BS_SOLID,
		LbColor: rgb,
	})
}

func fillRect(hdc win.HDC, brush win.HBRUSH, left, top, right, bottom int32) {
	oldBrush := win.SelectObject(hdc, win.HGDIOBJ(brush))
	oldPen := win.SelectObject(hdc, win.GetStockObject(win.NULL_PEN))
	win.Rectangle_(hdc, left, top, right, bottom)
	win.SelectObject(hdc, oldPen)
	win.SelectObject(hdc, oldBrush)
}

func drawRectBorder(hdc win.HDC, rect win.RECT, color win.COLORREF) {
	brush := createSolidBrush(color)
	if brush == 0 {
		return
	}
	defer win.DeleteObject(win.HGDIOBJ(brush))
	fillRect(hdc, brush, rect.Left, rect.Top, rect.Right, rect.Top+1)
	fillRect(hdc, brush, rect.Left, rect.Bottom-1, rect.Right, rect.Bottom)
	fillRect(hdc, brush, rect.Left, rect.Top, rect.Left+1, rect.Bottom)
	fillRect(hdc, brush, rect.Right-1, rect.Top, rect.Right, rect.Bottom)
}

func drawText(hdc win.HDC, text string, rect *win.RECT, format uint32) {
	win.DrawTextEx(hdc, syscall.StringToUTF16Ptr(text), -1, rect, format, nil)
}

func loadAppIcon(instance win.HINSTANCE) win.HICON {
	if icon := win.LoadIcon(instance, win.MAKEINTRESOURCE(1)); icon != 0 {
		return icon
	}
	return win.LoadIcon(0, win.MAKEINTRESOURCE(win.IDI_APPLICATION))
}

func enableDarkTitleBar(hwnd win.HWND) {
	value := int32(1)
	setDwmAttribute(hwnd, dwmwaUseImmersiveDarkMode, &value)
	setDwmAttribute(hwnd, dwmwaUseImmersiveDarkModeOld, &value)
}

func setDwmAttribute(hwnd win.HWND, attr uintptr, value *int32) {
	if procDwmSetWindowAttribute.Find() != nil {
		return
	}
	procDwmSetWindowAttribute.Call(
		uintptr(hwnd),
		attr,
		uintptr(unsafe.Pointer(value)),
		unsafe.Sizeof(*value),
	)
}
