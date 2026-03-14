//go:build windows

package gui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
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
	buttonClassName = "OneKeyVEButton"
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
	idBlackModeCombo
	idBlurEdit
	idFeatherEdit
	idRun
	idPause
	idStop
	idOpenOutput
	idSummaryEdit
	idStatusStatic
	idLogEdit
	idProgressBar
)

const (
	msgLogUpdate    = win.WM_APP + 1
	msgRunDone      = win.WM_APP + 2
	msgSummaryReady = win.WM_APP + 3
	msgControlDone  = win.WM_APP + 4
)

const (
	autoSaveTimerID        = 1
	uiRefreshTimerID       = 2
	autoSaveIntervalMilli  = 30000
	uiRefreshIntervalMilli = 100
	configFileName         = "onekeyve_gui_config.json"
)

const (
	iconSmall                    = 0
	iconBig                      = 1
	dwmwaUseImmersiveDarkMode    = 20
	dwmwaUseImmersiveDarkModeOld = 19
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

	workDirEdit    win.HWND
	outputDirEdit  win.HWND
	ffmpegEdit     win.HWND
	ffprobeEdit    win.HWND
	encoderCombo   win.HWND
	blackModeCombo win.HWND
	blurEdit       win.HWND
	featherEdit    win.HWND
	summaryEdit    win.HWND
	statusStatic   win.HWND
	logEdit        win.HWND
	progressBar    win.HWND
	runButton      win.HWND
	pauseButton    win.HWND
	stopButton     win.HWND

	fontNormal win.HFONT
	fontTitle  win.HFONT
	fontMono   win.HFONT

	bgBrush     win.HBRUSH
	panelBrush  win.HBRUSH
	headerBrush win.HBRUSH
	editBrush   win.HBRUSH
	accentBrush win.HBRUSH

	mu                sync.Mutex
	logLines          []string
	statusText        string
	summary           string
	progress          int
	running           bool
	paused            bool
	stopping          bool
	runStopped        bool
	runErr            error
	controller        *appcore.RunController
	controlBusy       bool
	refreshPending    bool
	renderedLog       string
	renderedStatus    string
	renderedProgress  int
	configPath        string
	lastConfigSaveErr string
	summarySeq        uint64
	summaryResult     summaryResult
	controlResult     controlResult
	closed            bool
}

type persistedConfig struct {
	WorkDir               string `json:"work_dir"`
	OutputDir             string `json:"output_dir"`
	FFmpegPath            string `json:"ffmpeg_path"`
	FFprobePath           string `json:"ffprobe_path"`
	Encoder               string `json:"encoder"`
	BlackBorderMode       string `json:"black_border_mode"`
	BlurSigma             int    `json:"blur_sigma"`
	FeatherPx             int    `json:"feather_px"`
	BlackLineThreshold    int    `json:"black_line_threshold"`
	BlackLineRatioPercent int    `json:"black_line_ratio_percent"`
	BlackLineRequiredRun  int    `json:"black_line_required_run"`
}

type buttonVariant int

const (
	buttonVariantSecondary buttonVariant = iota
	buttonVariantPrimary
	buttonVariantDanger
)

type buttonState struct {
	variant  buttonVariant
	hovered  bool
	pressed  bool
	tracking bool
}

type summarySnapshot struct {
	workDir     string
	outputDir   string
	ffmpegPath  string
	ffprobePath string
	encoder     string
	blackMode   string
}

type summaryResult struct {
	seq            uint64
	text           string
	showCompletion bool
}

type controlResult struct {
	action string
	paused bool
	err    error
}

var (
	globalState    *uiState
	buttonStates   = map[win.HWND]*buttonState{}
	buttonStatesMu sync.Mutex
)

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
	state.requestSummaryRefresh(false)

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
	configPath := configFilePath()
	return &uiState{
		bgBrush:     createSolidBrush(win.RGB(30, 30, 30)),
		panelBrush:  createSolidBrush(win.RGB(37, 37, 38)),
		headerBrush: createSolidBrush(win.RGB(45, 45, 48)),
		editBrush:   createSolidBrush(win.RGB(30, 30, 30)),
		accentBrush: createSolidBrush(win.RGB(14, 99, 156)),
		statusText:  "\u51c6\u5907\u5c31\u7eea",
		configPath:  configPath,
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
	return registerButtonClass(instance)
}

func registerButtonClass(instance win.HINSTANCE) error {
	var wc win.WNDCLASSEX
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	wc.LpfnWndProc = syscall.NewCallback(buttonWndProc)
	wc.HInstance = instance
	wc.HCursor = win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_HAND))
	wc.HbrBackground = 0
	wc.LpszClassName = syscall.StringToUTF16Ptr(buttonClassName)
	if win.RegisterClassEx(&wc) == 0 {
		return fmt.Errorf("register button class failed")
	}
	return nil
}

func wndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) (result uintptr) {
	state := globalState
	defer recoverWindowCallback(state, "wndProc", &result)

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
	case msgSummaryReady:
		if state != nil {
			state.applySummaryResult()
		}
		return 0
	case msgControlDone:
		if state != nil {
			state.finishControlAction()
		}
		return 0
	case win.WM_TIMER:
		if state != nil {
			switch wParam {
			case autoSaveTimerID:
				state.handleConfigSaveResult(state.saveCurrentConfig())
			case uiRefreshTimerID:
				state.refreshLog()
			}
		}
		return 0
	case win.WM_CTLCOLORSTATIC:
		if state != nil {
			hdc := win.HDC(wParam)
			win.SetBkMode(hdc, win.TRANSPARENT)
			win.SetTextColor(hdc, win.RGB(204, 204, 204))
			return uintptr(state.panelBrush)
		}
	case win.WM_CTLCOLORLISTBOX:
		if state != nil {
			hdc := win.HDC(wParam)
			win.SetBkColor(hdc, win.RGB(30, 30, 30))
			win.SetTextColor(hdc, win.RGB(212, 212, 212))
			return uintptr(state.editBrush)
		}
	case win.WM_CTLCOLOREDIT:
		if state != nil {
			hdc := win.HDC(wParam)
			win.SetBkColor(hdc, win.RGB(30, 30, 30))
			win.SetTextColor(hdc, win.RGB(212, 212, 212))
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
		if state != nil {
			saveErr := state.saveCurrentConfig()
			state.handleConfigSaveResult(saveErr)
			if saveErr != nil {
				messageBox(hwnd, "\u914d\u7f6e\u4fdd\u5b58\u5931\u8d25", saveErr.Error(), win.MB_ICONWARNING)
			}
		}
		win.DestroyWindow(hwnd)
		return 0
	case win.WM_DESTROY:
		if state != nil {
			state.mu.Lock()
			state.closed = true
			controller := state.controller
			state.hwnd = 0
			state.mu.Unlock()
			win.KillTimer(hwnd, autoSaveTimerID)
			win.KillTimer(hwnd, uiRefreshTimerID)
			if controller != nil {
				_ = controller.RequestStop()
			}
			state.destroyResources()
		}
		win.PostQuitMessage(0)
		return 0
	}

	return win.DefWindowProc(hwnd, msg, wParam, lParam)
}

func buttonWndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) (result uintptr) {
	state, ok := loadButtonState(hwnd)
	defer recoverWindowCallback(globalState, "buttonWndProc", &result)
	switch msg {
	case win.WM_MOUSEMOVE:
		if ok && !state.hovered {
			state.hovered = true
			trackButtonMouseLeave(hwnd, state)
			win.InvalidateRect(hwnd, nil, true)
		}
		return 0
	case win.WM_MOUSELEAVE:
		if ok {
			state.hovered = false
			state.tracking = false
			win.InvalidateRect(hwnd, nil, true)
		}
		return 0
	case win.WM_LBUTTONDOWN:
		if ok && win.IsWindowEnabled(hwnd) {
			state.pressed = true
			win.SetCapture(hwnd)
			win.SetFocus(hwnd)
			win.InvalidateRect(hwnd, nil, true)
		}
		return 0
	case win.WM_LBUTTONUP:
		if ok && state.pressed {
			state.pressed = false
			win.ReleaseCapture()
			win.InvalidateRect(hwnd, nil, true)
			if win.IsWindowEnabled(hwnd) && pointInClientRect(hwnd, lParam) {
				parent := win.GetParent(hwnd)
				if parent != 0 {
					id := uintptr(win.GetWindowLongPtr(hwnd, win.GWLP_ID))
					win.PostMessage(parent, win.WM_COMMAND, id, uintptr(hwnd))
				}
			}
		}
		return 0
	case win.WM_CAPTURECHANGED:
		if ok && state.pressed {
			state.pressed = false
			win.InvalidateRect(hwnd, nil, true)
		}
		return 0
	case win.WM_ENABLE, win.WM_SETTEXT, win.WM_SETFOCUS, win.WM_KILLFOCUS:
		result := win.DefWindowProc(hwnd, msg, wParam, lParam)
		win.InvalidateRect(hwnd, nil, true)
		return result
	case win.WM_KEYDOWN:
		if !win.IsWindowEnabled(hwnd) {
			return 0
		}
		switch wParam {
		case win.VK_SPACE, win.VK_RETURN:
			if ok && !state.pressed {
				state.pressed = true
				win.InvalidateRect(hwnd, nil, true)
			}
			return 0
		}
	case win.WM_KEYUP:
		if !win.IsWindowEnabled(hwnd) {
			return 0
		}
		switch wParam {
		case win.VK_SPACE, win.VK_RETURN:
			if ok {
				state.pressed = false
				win.InvalidateRect(hwnd, nil, true)
			}
			parent := win.GetParent(hwnd)
			if parent != 0 {
				id := uintptr(win.GetWindowLongPtr(hwnd, win.GWLP_ID))
				win.PostMessage(parent, win.WM_COMMAND, id, uintptr(hwnd))
			}
			return 0
		}
	case win.WM_GETDLGCODE:
		return win.DLGC_BUTTON
	case win.WM_ERASEBKGND:
		return 1
	case win.WM_PAINT:
		paintThemedButton(hwnd, state)
		return 0
	case win.WM_NCDESTROY:
		deleteButtonState(hwnd)
	}

	return win.DefWindowProc(hwnd, msg, wParam, lParam)
}

func (s *uiState) initControls() {
	s.fontNormal = createFont(18, 400, "Segoe UI")
	s.fontTitle = createFont(32, 700, "Segoe UI Semibold")
	s.fontMono = createFont(17, 400, "Consolas")
	defaultCfg := appcore.DefaultConfig(preferredWorkDir(currentWorkingDir()))
	saved := s.loadSavedConfig(defaultCfg)
	workDir := saved.WorkDir
	outputDir := saved.OutputDir
	createLabel(s.hwnd, "\u89c6\u9891\u76ee\u5f55", 28, 118, 140, 24, s.fontNormal)
	s.workDirEdit = createEdit(s.hwnd, idWorkDirEdit, workDir, 28, 146, 360, 30)
	createButton(s.hwnd, idWorkDirBrowse, "\u6d4f\u89c8\u76ee\u5f55", 398, 146, 104, 30, buttonVariantSecondary)
	createLabel(s.hwnd, "\u6839\u76ee\u5f55\u8f93\u51fa", 28, 190, 160, 24, s.fontNormal)
	s.outputDirEdit = createEdit(s.hwnd, idOutputDirEdit, outputDir, 28, 218, 360, 30)
	createButton(s.hwnd, idOutputDirBrowse, "\u6d4f\u89c8\u76ee\u5f55", 398, 218, 104, 30, buttonVariantSecondary)
	createLabel(s.hwnd, "FFmpeg", 28, 262, 100, 24, s.fontNormal)
	s.ffmpegEdit = createEdit(s.hwnd, idFFmpegEdit, saved.FFmpegPath, 28, 290, 360, 30)
	createButton(s.hwnd, idFFmpegBrowse, "\u9009\u62e9\u6587\u4ef6", 398, 290, 104, 30, buttonVariantSecondary)
	createLabel(s.hwnd, "FFprobe", 28, 334, 100, 24, s.fontNormal)
	s.ffprobeEdit = createEdit(s.hwnd, idFFprobeEdit, saved.FFprobePath, 28, 362, 360, 30)
	createButton(s.hwnd, idFFprobeBrowse, "\u9009\u62e9\u6587\u4ef6", 398, 362, 104, 30, buttonVariantSecondary)
	createButton(s.hwnd, idUseCFFmpeg, "\u4f7f\u7528 C:\\ffmpeg", 28, 406, 160, 32, buttonVariantSecondary)
	createButton(s.hwnd, idScan, "\u626b\u63cf\u73af\u5883", 198, 406, 120, 32, buttonVariantSecondary)
	createLabel(s.hwnd, "\u7f16\u7801\u5668", 28, 462, 100, 24, s.fontNormal)
	s.encoderCombo = createComboBox(s.hwnd, idEncoderCombo, 28, 490, 150, 240)
	addComboItems(s.encoderCombo, []string{"h264_nvenc", "libx264", "\u81ea\u52a8"})
	setComboSelectionByText(s.encoderCombo, saved.Encoder)
	createLabel(s.hwnd, "\u53bb\u9ed1\u8fb9\u65b9\u5f0f", 198, 462, 140, 24, s.fontNormal)
	s.blackModeCombo = createComboBox(s.hwnd, idBlackModeCombo, 198, 490, 180, 240)
	addComboItems(s.blackModeCombo, []string{ffmpeg.BlackBorderModeCenterCrop, ffmpeg.BlackBorderModeLegacy})
	setComboSelectionByText(s.blackModeCombo, saved.BlackBorderMode)
	createLabel(s.hwnd, "\u80cc\u666f\u6a21\u7cca", 388, 462, 120, 24, s.fontNormal)
	s.blurEdit = createEdit(s.hwnd, idBlurEdit, strconv.Itoa(saved.BlurSigma), 388, 490, 90, 30)
	createLabel(s.hwnd, "\u7fbd\u5316\u50cf\u7d20", 28, 534, 120, 24, s.fontNormal)
	s.featherEdit = createEdit(s.hwnd, idFeatherEdit, strconv.Itoa(saved.FeatherPx), 28, 562, 90, 30)
	s.runButton = createButton(s.hwnd, idRun, "\u5f00\u59cb\u5904\u7406", 28, 614, 140, 42, buttonVariantPrimary)
	s.pauseButton = createButton(s.hwnd, idPause, "\u6682\u505c\u5904\u7406", 178, 614, 120, 42, buttonVariantSecondary)
	s.stopButton = createButton(s.hwnd, idStop, "\u505c\u6b62\u5904\u7406", 308, 614, 116, 42, buttonVariantDanger)
	createButton(s.hwnd, idOpenOutput, "\u6253\u5f00\u6839\u76ee\u5f55\u8f93\u51fa", 28, 664, 220, 38, buttonVariantSecondary)
	createLabel(s.hwnd, "\u8fd0\u884c\u72b6\u6001", 28, 716, 100, 24, s.fontNormal)
	s.statusStatic = createLabel(s.hwnd, s.statusText, 28, 744, 474, 48, s.fontNormal)
	s.progressBar = createProgressBar(s.hwnd, idProgressBar, 28, 802, 474, 24)
	win.SendMessage(s.progressBar, win.PBM_SETRANGE32, 0, 1000)
	win.SendMessage(s.progressBar, win.PBM_SETBARCOLOR, 0, uintptr(win.RGB(14, 99, 156)))
	win.SendMessage(s.progressBar, win.PBM_SETBKCOLOR, 0, uintptr(win.RGB(45, 45, 48)))
	win.ShowWindow(s.progressBar, win.SW_HIDE)
	win.EnableWindow(s.pauseButton, false)
	win.EnableWindow(s.stopButton, false)
	createLabel(s.hwnd, "\u73af\u5883\u6458\u8981", 540, 118, 180, 24, s.fontNormal)
	s.summaryEdit = createReadOnlyEdit(s.hwnd, idSummaryEdit, "", 540, 146, 736, 176)
	createLabel(s.hwnd, "\u8fd0\u884c\u65e5\u5fd7", 540, 350, 100, 24, s.fontNormal)
	s.logEdit = createLogListBox(s.hwnd, idLogEdit, 540, 378, 736, 424)
	applyFont(s.logEdit, s.fontMono)
	win.SetTimer(s.hwnd, autoSaveTimerID, autoSaveIntervalMilli, 0)
	win.SetTimer(s.hwnd, uiRefreshTimerID, uiRefreshIntervalMilli, 0)
}
func (s *uiState) handleCommand(id uint16) {
	switch int(id) {
	case idWorkDirBrowse:
		if selected, ok := chooseFolder(getWindowText(s.workDirEdit)); ok {
			setWindowText(s.workDirEdit, selected)
			if strings.TrimSpace(getWindowText(s.outputDirEdit)) == "" {
				setWindowText(s.outputDirEdit, filepath.Join(selected, "output-gui"))
			}
			s.requestSummaryRefresh(false)
		}
	case idOutputDirBrowse:
		if selected, ok := chooseFolder(getWindowText(s.outputDirEdit)); ok {
			setWindowText(s.outputDirEdit, selected)
			s.requestSummaryRefresh(false)
		}
	case idFFmpegBrowse:
		if selected, ok := chooseFile(getWindowText(s.ffmpegEdit), "ffmpeg.exe|ffmpeg.exe|\u6240\u6709\u6587\u4ef6|*.*"); ok {
			setWindowText(s.ffmpegEdit, selected)
			s.requestSummaryRefresh(false)
		}
	case idFFprobeBrowse:
		if selected, ok := chooseFile(getWindowText(s.ffprobeEdit), "ffprobe.exe|ffprobe.exe|\u6240\u6709\u6587\u4ef6|*.*"); ok {
			setWindowText(s.ffprobeEdit, selected)
			s.requestSummaryRefresh(false)
		}
	case idUseCFFmpeg:
		setWindowText(s.ffmpegEdit, "C:\\ffmpeg\\bin\\ffmpeg.exe")
		setWindowText(s.ffprobeEdit, "C:\\ffmpeg\\bin\\ffprobe.exe")
		s.requestSummaryRefresh(false)
	case idScan:
		s.requestSummaryRefresh(true)
	case idEncoderCombo, idBlackModeCombo:
		s.requestSummaryRefresh(false)
	case idOpenOutput:
		outputDir := strings.TrimSpace(getWindowText(s.outputDirEdit))
		if outputDir == "" {
			messageBox(s.hwnd, "\u63d0\u793a", "\u6839\u76ee\u5f55\u8f93\u51fa\u4e0d\u80fd\u4e3a\u7a7a\u3002", win.MB_ICONINFORMATION)
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
	case idPause:
		s.togglePause()
	case idStop:
		s.stopRun()
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
	controller := appcore.NewRunController()
	cfg.Controller = controller
	cfg.OnLog = s.appendLog
	cfg.OnProgress = s.applyProgress
	s.mu.Lock()
	s.running = true
	s.paused = false
	s.stopping = false
	s.runStopped = false
	s.runErr = nil
	s.controller = controller
	s.controlBusy = false
	s.statusText = "\u5f00\u59cb\u5904\u7406"
	s.progress = 0
	s.logLines = nil
	s.refreshPending = true
	s.mu.Unlock()
	s.refreshButtons()
	win.ShowWindow(s.progressBar, win.SW_SHOW)
	win.SendMessage(s.progressBar, win.PBM_SETPOS, 0, 0)
	s.refreshLog()
	go func() {
		defer s.recoverAsync("run worker", controller, msgRunDone)
		s.appendLog("\u5f00\u59cb\u6267\u884c\u56fe\u5f62\u754c\u9762\u4efb\u52a1\u3002")
		s.appendLog("\u5de5\u4f5c\u76ee\u5f55: " + cfg.WorkDir)
		s.appendLog("\u8f93\u51fa\u76ee\u5f55: " + cfg.OutputDir)
		err := appcore.Run(cfg)
		s.mu.Lock()
		s.running = false
		s.paused = false
		s.stopping = false
		s.runErr = err
		s.controller = nil
		s.controlBusy = false
		if errors.Is(err, appcore.ErrStopped) {
			s.runStopped = true
			s.statusText = "\u5904\u7406\u5df2\u505c\u6b62"
		} else if err != nil {
			s.statusText = "\u5904\u7406\u5931\u8d25"
			s.progress = 0
		} else {
			s.statusText = "\u5904\u7406\u5b8c\u6210"
			s.progress = 1000
			s.prependLogLocked("\u5168\u90e8\u5904\u7406\u5b8c\u6210\u3002")
		}
		s.requestRefreshLocked()
		s.mu.Unlock()
		postAppMessage(s, msgRunDone)
	}()
}

func (s *uiState) togglePause() {
	s.mu.Lock()
	if !s.running || s.controller == nil || s.stopping || s.controlBusy {
		s.mu.Unlock()
		return
	}
	controller := s.controller
	nextPaused := !s.paused
	s.controlBusy = true
	if nextPaused {
		s.statusText = "\u6b63\u5728\u6682\u505c"
	} else {
		s.statusText = "\u6b63\u5728\u7ee7\u7eed"
	}
	s.requestRefreshLocked()
	s.mu.Unlock()

	s.refreshButtons()
	go func() {
		defer s.recoverAsync("pause worker", controller, msgControlDone)
		err := controller.SetPaused(nextPaused)
		s.mu.Lock()
		s.controlResult = controlResult{
			action: "pause",
			paused: nextPaused,
			err:    err,
		}
		s.mu.Unlock()
		postAppMessage(s, msgControlDone)
	}()
}

func (s *uiState) stopRun() {
	s.mu.Lock()
	if !s.running || s.controller == nil || s.stopping || s.controlBusy {
		s.mu.Unlock()
		return
	}
	controller := s.controller
	s.stopping = true
	s.controlBusy = true
	s.paused = false
	s.statusText = "\u6b63\u5728\u505c\u6b62"
	s.prependLogLocked("\u7528\u6237\u53d1\u51fa\u505c\u6b62\u8bf7\u6c42\u3002")
	s.requestRefreshLocked()
	s.mu.Unlock()

	s.refreshButtons()
	go func() {
		defer s.recoverAsync("stop worker", controller, msgControlDone)
		err := controller.RequestStop()
		s.mu.Lock()
		s.controlResult = controlResult{
			action: "stop",
			err:    err,
		}
		s.mu.Unlock()
		postAppMessage(s, msgControlDone)
	}()
}

func (s *uiState) finishRun() {
	s.refreshLog()
	s.refreshButtons()
	if s.runStopped {
		win.ShowWindow(s.progressBar, win.SW_HIDE)
		messageBox(s.hwnd, "\u5df2\u505c\u6b62", "\u5f53\u524d\u4efb\u52a1\u5df2\u6309\u7528\u6237\u8bf7\u6c42\u505c\u6b62\u3002", win.MB_ICONINFORMATION)
		return
	}
	if s.runErr != nil {
		win.ShowWindow(s.progressBar, win.SW_HIDE)
		messageBox(s.hwnd, "\u5904\u7406\u5931\u8d25", s.runErr.Error(), win.MB_ICONERROR)
		return
	}
	win.SendMessage(s.progressBar, win.PBM_SETPOS, 1000, 0)
	messageBox(s.hwnd, "\u5b8c\u6210", "\u6240\u6709\u4efb\u52a1\u5df2\u7ecf\u5904\u7406\u5b8c\u6210\u3002", win.MB_ICONINFORMATION)
}

func (s *uiState) finishControlAction() {
	s.mu.Lock()
	result := s.controlResult
	s.controlResult = controlResult{}
	s.controlBusy = false

	switch result.action {
	case "pause":
		if result.err == nil {
			s.paused = result.paused
			if result.paused {
				s.statusText = "\u5904\u7406\u5df2\u6682\u505c"
				s.prependLogLocked("\u7528\u6237\u5df2\u6682\u505c\u5f53\u524d\u4efb\u52a1\u3002")
			} else {
				s.statusText = "\u7ee7\u7eed\u5904\u7406"
				s.prependLogLocked("\u7528\u6237\u5df2\u7ee7\u7eed\u5f53\u524d\u4efb\u52a1\u3002")
			}
			s.requestRefreshLocked()
		} else if !errors.Is(result.err, appcore.ErrStopped) {
			s.statusText = "\u64cd\u4f5c\u5931\u8d25"
			s.requestRefreshLocked()
		}
	case "stop":
		if result.err != nil {
			s.stopping = false
			s.statusText = "\u505c\u6b62\u5931\u8d25"
			s.requestRefreshLocked()
		}
	}
	s.mu.Unlock()

	s.refreshButtons()

	switch result.action {
	case "pause":
		if result.err != nil && !errors.Is(result.err, appcore.ErrStopped) {
			messageBox(s.hwnd, "\u64cd\u4f5c\u5931\u8d25", result.err.Error(), win.MB_ICONERROR)
		}
	case "stop":
		if result.err != nil {
			messageBox(s.hwnd, "\u64cd\u4f5c\u5931\u8d25", result.err.Error(), win.MB_ICONERROR)
		}
	}
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
		return appcore.Config{}, fmt.Errorf("\u6839\u76ee\u5f55\u8f93\u51fa\u4e0d\u80fd\u4e3a\u7a7a")
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
	if encoder == "\u81ea\u52a8" {
		cfg.Encoder = ""
	} else {
		cfg.Encoder = encoder
	}
	cfg.BlackBorderMode = getComboSelection(s.blackModeCombo)
	return cfg, nil
}
func (s *uiState) requestSummaryRefresh(showCompletion bool) {
	if s == nil || s.hwnd == 0 {
		return
	}

	snapshot := s.captureSummarySnapshot()

	s.mu.Lock()
	s.summarySeq++
	seq := s.summarySeq
	s.summary = "\u6b63\u5728\u626b\u63cf\u89c6\u9891\u548c\u7ec4\u4ef6..."
	s.mu.Unlock()
	setWindowText(s.summaryEdit, "\u6b63\u5728\u626b\u63cf\u89c6\u9891\u548c\u7ec4\u4ef6...")

	go func() {
		defer s.recoverAsync("summary worker", nil, 0)
		text := buildSummary(snapshot)
		s.mu.Lock()
		if seq != s.summarySeq {
			s.mu.Unlock()
			return
		}
		s.summaryResult = summaryResult{
			seq:            seq,
			text:           text,
			showCompletion: showCompletion,
		}
		s.mu.Unlock()
		postAppMessage(s, msgSummaryReady)
	}()
}

func (s *uiState) applySummaryResult() {
	s.mu.Lock()
	result := s.summaryResult
	if result.seq != s.summarySeq || result.text == "" {
		s.mu.Unlock()
		return
	}
	s.summary = result.text
	s.summaryResult = summaryResult{}
	s.mu.Unlock()

	setWindowText(s.summaryEdit, result.text)
	if result.showCompletion {
		messageBox(s.hwnd, "\u626b\u63cf\u5b8c\u6210", result.text, win.MB_ICONINFORMATION)
	}
}

func (s *uiState) captureSummarySnapshot() summarySnapshot {
	return summarySnapshot{
		workDir:     strings.TrimSpace(getWindowText(s.workDirEdit)),
		outputDir:   strings.TrimSpace(getWindowText(s.outputDirEdit)),
		ffmpegPath:  strings.TrimSpace(getWindowText(s.ffmpegEdit)),
		ffprobePath: strings.TrimSpace(getWindowText(s.ffprobeEdit)),
		encoder:     getComboSelection(s.encoderCombo),
		blackMode:   getComboSelection(s.blackModeCombo),
	}
}

func buildSummary(snapshot summarySnapshot) string {
	discoveryCfg := appcore.DefaultConfig(snapshot.workDir)
	discoveryCfg.OutputDir = snapshot.outputDir
	videos, videoErr := appcore.DiscoverVideos(discoveryCfg)
	bins, binErr := ffmpeg.Locate(snapshot.workDir, ffmpeg.Binaries{
		FFmpeg:  snapshot.ffmpegPath,
		FFprobe: snapshot.ffprobePath,
	})

	lines := []string{}
	if videoErr != nil {
		lines = append(lines, "\u89c6\u9891\u626b\u63cf\u5931\u8d25: "+videoErr.Error())
	} else {
		lines = append(lines, fmt.Sprintf("\u68c0\u6d4b\u5230 %d \u4e2a\u89c6\u9891\u6587\u4ef6", len(videos)))
		rootVideos, nestedVideos := countDiscoveredVideos(snapshot.workDir, videos)
		lines = append(lines, fmt.Sprintf("\u6839\u76ee\u5f55\u89c6\u9891: %d \u4e2a", rootVideos))
		lines = append(lines, fmt.Sprintf("\u5b50\u76ee\u5f55\u89c6\u9891: %d \u4e2a", nestedVideos))
	}
	if binErr != nil {
		lines = append(lines, "\u7ec4\u4ef6\u5b9a\u4f4d\u5931\u8d25: "+binErr.Error())
	} else {
		lines = append(lines, "FFmpeg: "+bins.FFmpeg)
		lines = append(lines, "FFprobe: "+bins.FFprobe)
		lines = append(lines, "\u6765\u6e90: "+bins.Source)
	}
	lines = append(lines, "\u6839\u76ee\u5f55\u89c6\u9891\u8f93\u51fa: "+snapshot.outputDir+"\\<\u6bd4\u4f8b\u540d>")
	lines = append(lines, "\u5b50\u76ee\u5f55\u89c6\u9891\u8f93\u51fa: \u89c6\u9891\u6240\u5728\u76ee\u5f55\\<\u6bd4\u4f8b\u540d>")
	lines = append(lines, "\u7f16\u7801\u7b56\u7565: "+snapshot.encoder)
	lines = append(lines, "\u53bb\u9ed1\u8fb9\u65b9\u5f0f: "+snapshot.blackMode)
	lines = append(lines, "\u8bf4\u660e: \u5141\u8bb8\u8bfb\u53d6 C:\\ffmpeg\uff0c\u4f46\u4e0d\u4f1a\u628a\u8f93\u51fa\u5199\u5165 C \u76d8\u3002")
	return strings.Join(lines, "\r\n")
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
	if s.hwnd == 0 || s.closed {
		return
	}
	s.refreshPending = true
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
	if !s.refreshPending {
		s.mu.Unlock()
		return
	}
	s.refreshPending = false
	logText := strings.Join(s.logLines, "\n")
	logLines := append([]string(nil), s.logLines...)
	statusText := s.statusText
	progress := s.progress
	s.mu.Unlock()
	if logText != s.renderedLog {
		setListBoxLines(s.logEdit, logLines)
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

func (s *uiState) refreshButtons() {
	s.mu.Lock()
	running := s.running
	paused := s.paused
	stopping := s.stopping
	controlBusy := s.controlBusy
	s.mu.Unlock()

	win.EnableWindow(s.runButton, !running)
	win.EnableWindow(s.pauseButton, running && !stopping && !controlBusy)
	win.EnableWindow(s.stopButton, running && !stopping && !controlBusy)
	if paused {
		setWindowText(s.pauseButton, "\u7ee7\u7eed\u5904\u7406")
	} else {
		setWindowText(s.pauseButton, "\u6682\u505c\u5904\u7406")
	}
}
func (s *uiState) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *uiState) destroyResources() {
	if s == nil {
		return
	}
	deleteGDIObject(&s.fontNormal)
	deleteGDIObject(&s.fontTitle)
	deleteGDIObject(&s.fontMono)
	deleteGDIObject(&s.bgBrush)
	deleteGDIObject(&s.panelBrush)
	deleteGDIObject(&s.headerBrush)
	deleteGDIObject(&s.editBrush)
	deleteGDIObject(&s.accentBrush)
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
	oldColor := win.SetTextColor(hdc, win.RGB(255, 255, 255))
	oldMode := win.SetBkMode(hdc, win.TRANSPARENT)
	titleRect := win.RECT{Left: 28, Top: 18, Right: 600, Bottom: 62}
	drawText(hdc, "OneKeyVE", &titleRect, win.DT_LEFT|win.DT_VCENTER|win.DT_SINGLELINE)
	win.SelectObject(hdc, oldFont)
	win.SelectObject(hdc, win.HGDIOBJ(s.fontNormal))
	win.SetTextColor(hdc, win.RGB(156, 163, 175))
	subRect := win.RECT{Left: 30, Top: 56, Right: 560, Bottom: 80}
	drawText(hdc, "\u89c6\u9891\u6279\u5904\u7406\u684c\u9762\u7248", &subRect, win.DT_LEFT|win.DT_VCENTER|win.DT_SINGLELINE)
	win.SetTextColor(hdc, win.RGB(181, 181, 181))
	rightRect := win.RECT{Left: rect.Right - 420, Top: 38, Right: rect.Right - 30, Bottom: 70}
	drawText(hdc, "\u6df1\u8272\u754c\u9762 \u00b7 \u5b9e\u65f6\u65e5\u5fd7 \u00b7 \u76ee\u5f55\u76f4\u8fbe", &rightRect, win.DT_RIGHT|win.DT_VCENTER|win.DT_SINGLELINE)
	win.SetTextColor(hdc, oldColor)
	win.SetBkMode(hdc, oldMode)
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

func createButton(parent win.HWND, id int, text string, x, y, w, h int32, variant buttonVariant) win.HWND {
	hwnd := win.CreateWindowEx(
		0,
		syscall.StringToUTF16Ptr(buttonClassName),
		syscall.StringToUTF16Ptr(text),
		win.WS_CHILD|win.WS_VISIBLE|win.WS_TABSTOP,
		x, y, w, h,
		parent,
		win.HMENU(id),
		0,
		nil,
	)
	storeButtonState(hwnd, &buttonState{variant: variant})
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
	applyExplorerTheme(hwnd)
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
	applyExplorerTheme(hwnd)
	applyFont(hwnd, globalState.fontNormal)
	return hwnd
}

func createLogListBox(parent win.HWND, id int, x, y, w, h int32) win.HWND {
	hwnd := win.CreateWindowEx(
		win.WS_EX_CLIENTEDGE,
		syscall.StringToUTF16Ptr("LISTBOX"),
		nil,
		win.WS_CHILD|win.WS_VISIBLE|win.WS_VSCROLL|win.WS_HSCROLL|win.LBS_NOINTEGRALHEIGHT|win.LBS_DISABLENOSCROLL|win.LBS_NOTIFY,
		x, y, w, h,
		parent,
		win.HMENU(id),
		0,
		nil,
	)
	applyExplorerTheme(hwnd)
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
	applyExplorerTheme(hwnd)
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

func applyExplorerTheme(hwnd win.HWND) {
	if hwnd == 0 {
		return
	}
	win.SetWindowTheme(hwnd, syscall.StringToUTF16Ptr("Explorer"), nil)
}

func storeButtonState(hwnd win.HWND, state *buttonState) {
	buttonStatesMu.Lock()
	defer buttonStatesMu.Unlock()
	buttonStates[hwnd] = state
}

func loadButtonState(hwnd win.HWND) (*buttonState, bool) {
	buttonStatesMu.Lock()
	defer buttonStatesMu.Unlock()
	state, ok := buttonStates[hwnd]
	return state, ok
}

func deleteButtonState(hwnd win.HWND) {
	buttonStatesMu.Lock()
	defer buttonStatesMu.Unlock()
	delete(buttonStates, hwnd)
}

func trackButtonMouseLeave(hwnd win.HWND, state *buttonState) {
	if state == nil || state.tracking {
		return
	}
	var tme win.TRACKMOUSEEVENT
	tme.CbSize = uint32(unsafe.Sizeof(tme))
	tme.DwFlags = win.TME_LEAVE
	tme.HwndTrack = hwnd
	if win.TrackMouseEvent(&tme) {
		state.tracking = true
	}
}

func pointInClientRect(hwnd win.HWND, lParam uintptr) bool {
	var rect win.RECT
	if !win.GetClientRect(hwnd, &rect) {
		return false
	}
	x := int32(int16(uint16(lParam & 0xffff)))
	y := int32(int16(uint16((lParam >> 16) & 0xffff)))
	return x >= rect.Left && x < rect.Right && y >= rect.Top && y < rect.Bottom
}

func paintThemedButton(hwnd win.HWND, state *buttonState) {
	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(hwnd, &ps)
	defer win.EndPaint(hwnd, &ps)

	var rect win.RECT
	win.GetClientRect(hwnd, &rect)

	enabled := win.IsWindowEnabled(hwnd)
	bgColor, borderColor, textColor := themedButtonColors(state, enabled)

	fillSolidRect(hdc, rect, bgColor)
	drawRectBorder(hdc, rect, borderColor)

	if win.GetFocus() == hwnd {
		focusRect := rect
		focusRect.Left += 2
		focusRect.Top += 2
		focusRect.Right -= 2
		focusRect.Bottom -= 2
		drawRectBorder(hdc, focusRect, win.RGB(55, 148, 255))
	}

	oldMode := win.SetBkMode(hdc, win.TRANSPARENT)
	oldColor := win.SetTextColor(hdc, textColor)
	oldFont := win.SelectObject(hdc, win.HGDIOBJ(globalState.fontNormal))

	textRect := rect
	if state != nil && state.pressed {
		textRect.Left++
		textRect.Top++
		textRect.Right++
		textRect.Bottom++
	}
	drawText(hdc, getWindowText(hwnd), &textRect, win.DT_CENTER|win.DT_VCENTER|win.DT_SINGLELINE)

	win.SelectObject(hdc, oldFont)
	win.SetTextColor(hdc, oldColor)
	win.SetBkMode(hdc, oldMode)
}

func themedButtonColors(state *buttonState, enabled bool) (bg win.COLORREF, border win.COLORREF, text win.COLORREF) {
	if !enabled {
		return win.RGB(58, 58, 58), win.RGB(62, 62, 66), win.RGB(133, 133, 133)
	}

	variant := buttonVariantSecondary
	hovered := false
	pressed := false
	if state != nil {
		variant = state.variant
		hovered = state.hovered
		pressed = state.pressed
	}

	switch variant {
	case buttonVariantPrimary:
		switch {
		case pressed:
			return win.RGB(9, 71, 113), win.RGB(55, 148, 255), win.RGB(255, 255, 255)
		case hovered:
			return win.RGB(17, 119, 187), win.RGB(55, 148, 255), win.RGB(255, 255, 255)
		default:
			return win.RGB(14, 99, 156), win.RGB(0, 122, 204), win.RGB(255, 255, 255)
		}
	case buttonVariantDanger:
		switch {
		case pressed:
			return win.RGB(102, 29, 15), win.RGB(244, 135, 113), win.RGB(255, 255, 255)
		case hovered:
			return win.RGB(160, 48, 30), win.RGB(244, 135, 113), win.RGB(255, 255, 255)
		default:
			return win.RGB(122, 36, 22), win.RGB(214, 82, 66), win.RGB(255, 255, 255)
		}
	default:
		switch {
		case pressed:
			return win.RGB(43, 43, 43), win.RGB(110, 110, 110), win.RGB(220, 220, 220)
		case hovered:
			return win.RGB(69, 69, 70), win.RGB(142, 142, 142), win.RGB(255, 255, 255)
		default:
			return win.RGB(60, 60, 60), win.RGB(90, 93, 94), win.RGB(204, 204, 204)
		}
	}
}

func addComboItems(hwnd win.HWND, items []string) {
	for _, item := range items {
		win.SendMessage(hwnd, win.CB_ADDSTRING, 0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(item))))
	}
}

func setComboSelectionByText(hwnd win.HWND, text string) {
	count := int(win.SendMessage(hwnd, win.CB_GETCOUNT, 0, 0))
	for i := 0; i < count; i++ {
		buf := make([]uint16, 256)
		win.SendMessage(hwnd, win.CB_GETLBTEXT, uintptr(i), uintptr(unsafe.Pointer(&buf[0])))
		if syscall.UTF16ToString(buf) == text {
			win.SendMessage(hwnd, win.CB_SETCURSEL, uintptr(i), 0)
			return
		}
	}
	if count > 0 {
		win.SendMessage(hwnd, win.CB_SETCURSEL, 0, 0)
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

func setListBoxLines(hwnd win.HWND, lines []string) {
	if hwnd == 0 {
		return
	}
	win.SendMessage(hwnd, win.WM_SETREDRAW, 0, 0)
	win.SendMessage(hwnd, win.LB_RESETCONTENT, 0, 0)
	maxWidth := 0
	for _, line := range lines {
		win.SendMessage(hwnd, win.LB_ADDSTRING, 0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(line))))
		if width := approximateTextWidth(line); width > maxWidth {
			maxWidth = width
		}
	}
	win.SendMessage(hwnd, win.LB_SETHORIZONTALEXTENT, uintptr(maxWidth), 0)
	win.SendMessage(hwnd, win.WM_SETREDRAW, 1, 0)
	win.InvalidateRect(hwnd, nil, true)
	win.UpdateWindow(hwnd)
}

func approximateTextWidth(text string) int {
	length := len([]rune(text))
	if length == 0 {
		return 0
	}
	return length*9 + 24
}

func (s *uiState) loadSavedConfig(defaultCfg appcore.Config) persistedConfig {
	cfg := persistedConfig{
		WorkDir:               defaultCfg.WorkDir,
		OutputDir:             filepath.Join(defaultCfg.WorkDir, "output-gui"),
		FFmpegPath:            firstNonEmpty(os.Getenv("ONEKEYVE_FFMPEG"), os.Getenv("FFMPEG_PATH"), "C:\\ffmpeg\\bin\\ffmpeg.exe"),
		FFprobePath:           firstNonEmpty(os.Getenv("ONEKEYVE_FFPROBE"), os.Getenv("FFPROBE_PATH"), "C:\\ffmpeg\\bin\\ffprobe.exe"),
		Encoder:               defaultCfg.Encoder,
		BlackBorderMode:       defaultCfg.BlackBorderMode,
		BlurSigma:             defaultCfg.BlurSigma,
		FeatherPx:             defaultCfg.FeatherPx,
		BlackLineThreshold:    defaultCfg.BlackLineThreshold,
		BlackLineRatioPercent: defaultCfg.BlackLineRatioPercent,
		BlackLineRequiredRun:  defaultCfg.BlackLineRequiredRun,
	}
	if s == nil || strings.TrimSpace(s.configPath) == "" {
		return cfg
	}
	raw, err := os.ReadFile(s.configPath)
	if err != nil {
		return cfg
	}
	var saved persistedConfig
	if json.Unmarshal(raw, &saved) != nil {
		return cfg
	}
	if strings.TrimSpace(saved.WorkDir) != "" {
		cfg.WorkDir = saved.WorkDir
	}
	if strings.TrimSpace(saved.OutputDir) != "" {
		cfg.OutputDir = saved.OutputDir
	}
	if strings.TrimSpace(saved.FFmpegPath) != "" {
		cfg.FFmpegPath = saved.FFmpegPath
	}
	if strings.TrimSpace(saved.FFprobePath) != "" {
		cfg.FFprobePath = saved.FFprobePath
	}
	if strings.TrimSpace(saved.Encoder) != "" {
		cfg.Encoder = saved.Encoder
	}
	if strings.TrimSpace(saved.BlackBorderMode) != "" {
		cfg.BlackBorderMode = saved.BlackBorderMode
	}
	if saved.BlurSigma > 0 {
		cfg.BlurSigma = saved.BlurSigma
	}
	if saved.FeatherPx > 0 {
		cfg.FeatherPx = saved.FeatherPx
	}
	if saved.BlackLineThreshold >= 0 {
		cfg.BlackLineThreshold = saved.BlackLineThreshold
	}
	if saved.BlackLineRatioPercent > 0 {
		cfg.BlackLineRatioPercent = saved.BlackLineRatioPercent
	}
	if saved.BlackLineRequiredRun > 0 {
		cfg.BlackLineRequiredRun = saved.BlackLineRequiredRun
	}
	return cfg
}

func (s *uiState) saveCurrentConfig() error {
	if s == nil || s.hwnd == 0 || strings.TrimSpace(s.configPath) == "" {
		return nil
	}
	payload, err := json.MarshalIndent(s.capturePersistedConfig(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.configPath, payload, 0o644)
}

func (s *uiState) handleConfigSaveResult(err error) {
	if s == nil {
		return
	}
	if err == nil {
		s.mu.Lock()
		s.lastConfigSaveErr = ""
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	if s.lastConfigSaveErr == err.Error() {
		s.mu.Unlock()
		return
	}
	s.lastConfigSaveErr = err.Error()
	s.mu.Unlock()

	s.appendLog("save config failed: " + err.Error())
}

func (s *uiState) capturePersistedConfig() persistedConfig {
	defaultCfg := appcore.DefaultConfig(strings.TrimSpace(getWindowText(s.workDirEdit)))
	savedCfg := s.loadSavedConfig(defaultCfg)
	encoder := getComboSelection(s.encoderCombo)
	if encoder == "" {
		encoder = defaultCfg.Encoder
	}
	blackMode := getComboSelection(s.blackModeCombo)
	if blackMode == "" {
		blackMode = defaultCfg.BlackBorderMode
	}
	return persistedConfig{
		WorkDir:               strings.TrimSpace(getWindowText(s.workDirEdit)),
		OutputDir:             strings.TrimSpace(getWindowText(s.outputDirEdit)),
		FFmpegPath:            strings.TrimSpace(getWindowText(s.ffmpegEdit)),
		FFprobePath:           strings.TrimSpace(getWindowText(s.ffprobeEdit)),
		Encoder:               encoder,
		BlackBorderMode:       blackMode,
		BlurSigma:             parseIntOrDefault(getWindowText(s.blurEdit), defaultCfg.BlurSigma),
		FeatherPx:             parseIntOrDefault(getWindowText(s.featherEdit), defaultCfg.FeatherPx),
		BlackLineThreshold:    savedCfg.BlackLineThreshold,
		BlackLineRatioPercent: savedCfg.BlackLineRatioPercent,
		BlackLineRequiredRun:  savedCfg.BlackLineRequiredRun,
	}
}

func parseIntOrDefault(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func configFilePath() string {
	executablePath, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(executablePath), configFileName)
}

func chooseFolder(initial string) (string, bool) {
	displayName := make([]uint16, win.MAX_PATH)
	title := syscall.StringToUTF16Ptr("\u9009\u62e9\u76ee\u5f55")
	initialSelection := strings.TrimSpace(initial)
	callback := syscall.NewCallback(func(hwnd uintptr, msg uint32, lParam, data uintptr) uintptr {
		if msg == bffmInitialized && initialSelection != "" {
			win.SendMessage(
				win.HWND(hwnd),
				bffmSetSelectionW,
				1,
				uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(initialSelection))),
			)
		}
		return 0
	})
	bi := win.BROWSEINFO{
		HwndOwner:      globalState.hwnd,
		PszDisplayName: &displayName[0],
		LpszTitle:      title,
		UlFlags:        bifReturnOnlyFSDirs | bifEditBox | bifNewDialogStyle,
		Lpfn:           callback,
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

func postAppMessage(s *uiState, msg uint32) {
	if s == nil || msg == 0 {
		return
	}
	s.mu.Lock()
	hwnd := s.hwnd
	closed := s.closed
	s.mu.Unlock()
	if closed || hwnd == 0 {
		return
	}
	win.PostMessage(hwnd, msg, 0, 0)
}

func recoverWindowCallback(state *uiState, scope string, result *uintptr) {
	recovered := recover()
	if recovered == nil {
		return
	}
	handleUIPanic(state, scope, recovered)
	if result != nil {
		*result = 0
	}
}

func (s *uiState) recoverAsync(scope string, controller *appcore.RunController, notifyMsg uint32) {
	recovered := recover()
	if recovered == nil {
		return
	}
	handleUIPanic(s, scope, recovered)
	if controller != nil {
		_ = controller.RequestStop()
	}
	if notifyMsg != 0 {
		postAppMessage(s, notifyMsg)
	}
}

func handleUIPanic(state *uiState, scope string, recovered any) {
	message := fmt.Sprintf("%s panic: %v", scope, recovered)
	stack := strings.TrimSpace(string(debug.Stack()))
	if state == nil {
		return
	}

	state.mu.Lock()
	running := state.running
	controller := state.controller
	if running {
		state.running = false
		state.paused = false
		state.stopping = false
		state.controlBusy = false
		state.runStopped = false
		state.runErr = fmt.Errorf("%s", message)
		state.controller = nil
		state.statusText = "\u5904\u7406\u5931\u8d25"
		state.requestRefreshLocked()
	}
	state.mu.Unlock()

	state.appendLog(message)
	if stack != "" {
		state.appendLog(stack)
	}
	if controller != nil {
		_ = controller.RequestStop()
	}
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

func countDiscoveredVideos(workDir string, videos []string) (rootVideos int, nestedVideos int) {
	for _, videoPath := range videos {
		if sameFolderPath(filepath.Dir(videoPath), workDir) {
			rootVideos++
			continue
		}
		nestedVideos++
	}
	return rootVideos, nestedVideos
}

func sameFolderPath(a string, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func createSolidBrush(rgb win.COLORREF) win.HBRUSH {
	return win.CreateBrushIndirect(&win.LOGBRUSH{
		LbStyle: win.BS_SOLID,
		LbColor: rgb,
	})
}

func deleteGDIObject[T ~uintptr](handle *T) {
	if handle == nil || *handle == 0 {
		return
	}
	win.DeleteObject(win.HGDIOBJ(*handle))
	*handle = 0
}

func fillRect(hdc win.HDC, brush win.HBRUSH, left, top, right, bottom int32) {
	oldBrush := win.SelectObject(hdc, win.HGDIOBJ(brush))
	oldPen := win.SelectObject(hdc, win.GetStockObject(win.NULL_PEN))
	win.Rectangle_(hdc, left, top, right, bottom)
	win.SelectObject(hdc, oldPen)
	win.SelectObject(hdc, oldBrush)
}

func fillSolidRect(hdc win.HDC, rect win.RECT, color win.COLORREF) {
	brush := createSolidBrush(color)
	if brush == 0 {
		return
	}
	defer win.DeleteObject(win.HGDIOBJ(brush))
	fillRect(hdc, brush, rect.Left, rect.Top, rect.Right, rect.Bottom)
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
