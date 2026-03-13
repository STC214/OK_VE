package ffmpeg

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"onekeyvego/internal/procutil"
)

var (
	executablePathFunc = os.Executable
	lookPathFunc       = exec.LookPath
	driveRootsFunc     = listDriveRoots
	prepareRuntimeDir  = ensureRuntimeDirWritable
)

const (
	BlackBorderModeCenterCrop = "center_crop"
	BlackBorderModeLegacy     = "legacy"
)

type Binaries struct {
	FFmpeg  string
	FFprobe string
	Source  string
}

type VideoMeta struct {
	Width  int
	Height int
	Frames int
}

type CropDetection struct {
	Rect   CropRect
	Frames int
}

type ProcessHooks struct {
	Started  func(*os.Process) error
	Finished func(*os.Process)
}

type BlackBorderOptions struct {
	Mode           string
	LineThreshold  int
	LineRatio      float64
	RequiredRun    int
	SampleFPS      int
	SampleDuration int
	SampleFrameCap int
}

type diagnostics struct {
	Components map[string]struct {
		Path string `json:"path"`
	} `json:"components"`
}

func Locate(root string, overrides Binaries) (Binaries, error) {
	overrides = mergeEnvOverrides(overrides)
	if err := validateOverrides(overrides); err != nil {
		return Binaries{}, err
	}
	if overrides.FFmpeg != "" && overrides.FFprobe != "" {
		return Binaries{
			FFmpeg:  overrides.FFmpeg,
			FFprobe: overrides.FFprobe,
			Source:  "flags/env",
		}, nil
	}

	searches := []func(string) (Binaries, error){
		func(scanRoot string) (Binaries, error) { return fromEmbedded(scanRoot) },
		func(_ string) (Binaries, error) { return fromExecutableDir() },
		func(_ string) (Binaries, error) { return fromDriveRoots() },
		func(scanRoot string) (Binaries, error) { return fromFilesystem(scanRoot) },
		func(_ string) (Binaries, error) { return fromPath() },
		func(scanRoot string) (Binaries, error) { return fromDiagnostics(scanRoot) },
	}

	for _, search := range searches {
		bins, err := search(root)
		if err != nil {
			continue
		}
		bins = applyOverrides(bins, overrides)
		if bins.FFmpeg != "" && bins.FFprobe != "" {
			return bins, nil
		}
	}

	return Binaries{}, errors.New("ffmpeg not found in flags/env, embedded payloads, executable directory, drive-root ffmpeg folders, workspace scan, PATH, or diagnostics")
}

func DetectPreferredEncoder(ffmpegPath string, override string, hooks *ProcessHooks) string {
	override = strings.ToLower(strings.TrimSpace(override))
	if override == "libx264" {
		return "libx264"
	}
	if override != "" && override != "h264_nvenc" {
		return override
	}

	cmd := exec.Command(ffmpegPath, "-hide_banner", "-encoders")
	procutil.HideWindow(cmd)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := startWithHooks(cmd, hooks); err != nil {
		return "libx264"
	}
	err := cmd.Wait()
	finishWithHooks(cmd, hooks)
	if err != nil {
		if override == "h264_nvenc" {
			return "libx264"
		}
		return "libx264"
	}

	encoders := strings.ToLower(stdout.String())
	if override == "h264_nvenc" {
		if strings.Contains(encoders, "h264_nvenc") {
			return "h264_nvenc"
		}
		return "libx264"
	}
	if strings.Contains(encoders, "h264_nvenc") {
		return "h264_nvenc"
	}
	return "libx264"
}

func Probe(ffprobePath string, videoPath string, hooks *ProcessHooks) (VideoMeta, error) {
	cmd := exec.Command(
		ffprobePath,
		"-v", "error",
		"-select_streams", "v:0",
		"-count_frames",
		"-show_entries", "stream=width,height,nb_frames,nb_read_frames",
		"-of", "json",
		videoPath,
	)
	procutil.HideWindow(cmd)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := startWithHooks(cmd, hooks); err != nil {
		return VideoMeta{}, err
	}
	err := cmd.Wait()
	finishWithHooks(cmd, hooks)
	if err != nil {
		return VideoMeta{}, fmt.Errorf("ffprobe %s: %w (%s)", videoPath, err, strings.TrimSpace(stderr.String()))
	}

	var payload struct {
		Streams []struct {
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			Frames       string `json:"nb_frames"`
			ReadFrames   string `json:"nb_read_frames"`
			DisplayRatio string `json:"display_aspect_ratio"`
		} `json:"streams"`
	}

	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		return VideoMeta{}, fmt.Errorf("parse ffprobe output for %s: %w", videoPath, err)
	}
	if len(payload.Streams) == 0 {
		return VideoMeta{}, fmt.Errorf("ffprobe returned no video streams for %s", videoPath)
	}

	stream := payload.Streams[0]
	if stream.Width == 0 || stream.Height == 0 {
		return VideoMeta{}, fmt.Errorf("invalid dimensions for %s", videoPath)
	}

	return VideoMeta{
		Width:  stream.Width,
		Height: stream.Height,
		Frames: firstPositiveInt(stream.Frames, stream.ReadFrames),
	}, nil
}

func DetectBlackBorders(ffmpegPath string, videoPath string, sourceWidth int, sourceHeight int, opts BlackBorderOptions, hooks *ProcessHooks, onProgress func(processedFrames int)) (CropDetection, error) {
	opts = normalizeBlackBorderOptions(opts)
	if opts.Mode == BlackBorderModeLegacy {
		return detectBlackBordersLegacy(ffmpegPath, videoPath, sourceWidth, sourceHeight, hooks, onProgress)
	}
	return detectBlackBordersCenterCrop(ffmpegPath, videoPath, sourceWidth, sourceHeight, opts, hooks, onProgress)
}

func detectBlackBordersCenterCrop(ffmpegPath string, videoPath string, sourceWidth int, sourceHeight int, opts BlackBorderOptions, hooks *ProcessHooks, onProgress func(processedFrames int)) (CropDetection, error) {
	sampleWidth, sampleHeight := scaledSampleSize(sourceWidth, sourceHeight, 192)
	frameSize := sampleWidth * sampleHeight
	if frameSize <= 0 {
		return CropDetection{}, fmt.Errorf("invalid sample size %dx%d", sampleWidth, sampleHeight)
	}

	cmd := exec.Command(
		ffmpegPath,
		"-v", "error",
		"-i", videoPath,
		"-t", strconv.Itoa(opts.SampleDuration),
		"-vf", fmt.Sprintf("fps=%d,scale=%d:%d:flags=neighbor,format=gray", opts.SampleFPS, sampleWidth, sampleHeight),
		"-frames:v", strconv.Itoa(opts.SampleFrameCap),
		"-f", "rawvideo",
		"pipe:1",
	)
	procutil.HideWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return CropDetection{}, fmt.Errorf("open crop detection pipe: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := startWithHooks(cmd, hooks); err != nil {
		return CropDetection{}, err
	}
	defer finishWithHooks(cmd, hooks)

	raw, readErr := io.ReadAll(stdout)
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return CropDetection{}, fmt.Errorf("read crop detection frames: %w", readErr)
	}
	if err := cmd.Wait(); err != nil {
		return CropDetection{}, fmt.Errorf("sample frames for crop detection: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}

	frameCount := len(raw) / frameSize
	if frameCount == 0 {
		return CropDetection{}, errors.New("no frames available for crop detection")
	}

	reporter := newProgressReporter(onProgress, 1)
	topSamples := make([]int, 0, frameCount)
	bottomSamples := make([]int, 0, frameCount)
	leftSamples := make([]int, 0, frameCount)
	rightSamples := make([]int, 0, frameCount)

	for i := 0; i < frameCount; i++ {
		reporter.Report(i + 1)
		frame := raw[i*frameSize : (i+1)*frameSize]
		top, bottom, left, right, ok := detectFrameBorders(frame, sampleWidth, sampleHeight, opts)
		if !ok {
			continue
		}
		topSamples = append(topSamples, top)
		bottomSamples = append(bottomSamples, bottom)
		leftSamples = append(leftSamples, left)
		rightSamples = append(rightSamples, right)
	}

	validFrames := len(topSamples)
	if validFrames == 0 {
		return CropDetection{}, errors.New("no reliable frames for crop detection")
	}

	top := averageInt(topSamples)
	bottom := averageInt(bottomSamples)
	left := averageInt(leftSamples)
	right := averageInt(rightSamples)

	if top == 0 && bottom == 0 && left == 0 && right == 0 {
		return CropDetection{Frames: validFrames}, nil
	}

	rect := scaleCropRect(sampleWidth, sampleHeight, sourceWidth, sourceHeight, left, right, top, bottom)
	if !rect.HasCrop() {
		return CropDetection{Frames: validFrames}, nil
	}
	return CropDetection{Rect: rect, Frames: validFrames}, nil
}

func detectBlackBordersLegacy(ffmpegPath string, videoPath string, sourceWidth int, sourceHeight int, hooks *ProcessHooks, onProgress func(processedFrames int)) (CropDetection, error) {
	sampleWidth, sampleHeight := scaledSampleSize(sourceWidth, sourceHeight, 192)
	frameSize := sampleWidth * sampleHeight
	if frameSize <= 0 {
		return CropDetection{}, fmt.Errorf("invalid sample size %dx%d", sampleWidth, sampleHeight)
	}

	reporter := newProgressReporter(onProgress, 12)
	noBorderEarlyExit, earlyFrames, earlyErr := firstThreeSecondsHaveNoBordersLegacy(ffmpegPath, videoPath, sampleWidth, sampleHeight, hooks, reporter.Report)
	if earlyErr == nil && noBorderEarlyExit {
		reporter.Report(earlyFrames)
		return CropDetection{Frames: earlyFrames}, nil
	}

	cmd := exec.Command(
		ffmpegPath,
		"-v", "error",
		"-ss", "0.5",
		"-i", videoPath,
		"-vf", fmt.Sprintf("fps=2,scale=%d:%d:flags=area,format=gray", sampleWidth, sampleHeight),
		"-frames:v", "8",
		"-f", "rawvideo",
		"pipe:1",
	)
	procutil.HideWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return CropDetection{}, fmt.Errorf("open crop detection pipe: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := startWithHooks(cmd, hooks); err != nil {
		return CropDetection{}, err
	}
	defer finishWithHooks(cmd, hooks)

	raw, readErr := io.ReadAll(stdout)
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return CropDetection{}, fmt.Errorf("read crop detection frames: %w", readErr)
	}
	if err := cmd.Wait(); err != nil {
		return CropDetection{}, fmt.Errorf("sample frames for crop detection: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}

	frameCount := len(raw) / frameSize
	if frameCount == 0 {
		return CropDetection{}, errors.New("no frames available for crop detection")
	}

	topSamples := make([]int, 0, frameCount)
	bottomSamples := make([]int, 0, frameCount)
	leftSamples := make([]int, 0, frameCount)
	rightSamples := make([]int, 0, frameCount)

	for i := 0; i < frameCount; i++ {
		frame := raw[i*frameSize : (i+1)*frameSize]
		top, bottom, left, right, ok := detectFrameBordersLegacy(frame, sampleWidth, sampleHeight)
		if !ok {
			continue
		}
		topSamples = append(topSamples, top)
		bottomSamples = append(bottomSamples, bottom)
		leftSamples = append(leftSamples, left)
		rightSamples = append(rightSamples, right)
	}

	validFrames := len(topSamples)
	if validFrames == 0 {
		return CropDetection{}, errors.New("no reliable frames for crop detection")
	}

	top := medianInt(topSamples)
	bottom := medianInt(bottomSamples)
	left := medianInt(leftSamples)
	right := medianInt(rightSamples)

	sideLeft, sideRight, sideFrames, sideErr := detectPersistentSideBarsLegacy(ffmpegPath, videoPath, sampleWidth, sampleHeight, top, bottom, hooks, reporter.Report)
	if sideErr == nil {
		left = sideLeft
		right = sideRight
		if sideFrames > validFrames {
			validFrames = sideFrames
		}
	}

	if top == 0 && bottom == 0 && left == 0 && right == 0 {
		return CropDetection{Frames: validFrames}, nil
	}

	rect := scaleCropRectLegacy(sampleWidth, sampleHeight, sourceWidth, sourceHeight, left, right, top, bottom)
	if !rect.HasCrop() {
		return CropDetection{Frames: validFrames}, nil
	}
	return CropDetection{Rect: rect, Frames: validFrames}, nil
}

func firstThreeSecondsHaveNoBordersLegacy(ffmpegPath string, videoPath string, sampleWidth int, sampleHeight int, hooks *ProcessHooks, onProgress func(processedFrames int)) (bool, int, error) {
	cmd := exec.Command(
		ffmpegPath,
		"-v", "error",
		"-t", "3",
		"-i", videoPath,
		"-vf", fmt.Sprintf("scale=%d:%d:flags=area,format=gray", sampleWidth, sampleHeight),
		"-f", "rawvideo",
		"pipe:1",
	)
	procutil.HideWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, 0, fmt.Errorf("open early border detection pipe: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := startWithHooks(cmd, hooks); err != nil {
		return false, 0, err
	}
	defer finishWithHooks(cmd, hooks)

	frameSize := sampleWidth * sampleHeight
	frameBuf := make([]byte, frameSize)
	frames := 0

	for {
		_, readErr := io.ReadFull(stdout, frameBuf)
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			break
		}
		if readErr != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return false, frames, fmt.Errorf("read early border detection frames: %w", readErr)
		}

		frames++
		if onProgress != nil {
			onProgress(frames)
		}
		if frameHasBlackBorderLegacy(frameBuf, sampleWidth, sampleHeight) {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
			return false, frames, nil
		}
	}

	if err := cmd.Wait(); err != nil {
		return false, frames, fmt.Errorf("early border detection failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	if frames == 0 {
		return false, 0, errors.New("no frames available for early border detection")
	}
	return true, frames, nil
}

func frameHasBlackBorderLegacy(frame []byte, width int, height int) bool {
	top, bottom, left, right, ok := detectFrameBordersLegacy(frame, width, height)
	if !ok {
		return false
	}
	return top > 0 || bottom > 0 || left > 0 || right > 0
}

func detectPersistentSideBarsLegacy(ffmpegPath string, videoPath string, sampleWidth int, sampleHeight int, top int, bottom int, hooks *ProcessHooks, onProgress func(processedFrames int)) (left int, right int, frames int, err error) {
	const (
		sideMaxBrightRatio      = 0.18
		sideMaxAverageLuma      = 28.0
		persistentFrameFraction = 0.82
	)

	activeTop := top
	activeHeight := sampleHeight - top - bottom
	if activeHeight <= 0 {
		activeTop = 0
		activeHeight = sampleHeight
	}

	filter := fmt.Sprintf("scale=%d:%d:flags=area", sampleWidth, sampleHeight)
	if activeTop > 0 || activeHeight != sampleHeight {
		filter += fmt.Sprintf(",crop=%d:%d:0:%d", sampleWidth, activeHeight, activeTop)
	}
	filter += ",format=gray"

	cmd := exec.Command(
		ffmpegPath,
		"-v", "error",
		"-i", videoPath,
		"-vf", filter,
		"-f", "rawvideo",
		"pipe:1",
	)
	procutil.HideWindow(cmd)

	stdout, pipeErr := cmd.StdoutPipe()
	if pipeErr != nil {
		return 0, 0, 0, fmt.Errorf("open side-bar analysis pipe: %w", pipeErr)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := startWithHooks(cmd, hooks); err != nil {
		return 0, 0, 0, err
	}
	defer finishWithHooks(cmd, hooks)

	frameBuf := make([]byte, sampleWidth*activeHeight)
	blackCounts := make([]int, sampleWidth)

	for {
		_, readErr := io.ReadFull(stdout, frameBuf)
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			break
		}
		if readErr != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return 0, 0, frames, fmt.Errorf("read side-bar analysis frames: %w", readErr)
		}

		frames++
		if onProgress != nil {
			onProgress(frames)
		}
		updateColumnBlackCountsLegacy(frameBuf, sampleWidth, activeHeight, blackCounts, sideMaxBrightRatio, sideMaxAverageLuma)
	}

	if waitErr := cmd.Wait(); waitErr != nil {
		return 0, 0, frames, fmt.Errorf("side-bar analysis failed: %w (%s)", waitErr, strings.TrimSpace(stderr.String()))
	}
	if frames == 0 {
		return 0, 0, 0, errors.New("no frames available for side-bar analysis")
	}

	left = persistentEdgeSpanFromStartLegacy(blackCounts, frames, persistentFrameFraction)
	right = persistentEdgeSpanFromEndLegacy(blackCounts, frames, persistentFrameFraction)
	return left, right, frames, nil
}

func frameHasBlackBorder(frame []byte, width int, height int) bool {
	top, bottom, left, right, ok := detectFrameBorders(frame, width, height, normalizeBlackBorderOptions(BlackBorderOptions{}))
	if !ok {
		return false
	}
	return top > 0 || bottom > 0 || left > 0 || right > 0
}

type progressReporter struct {
	onProgress   func(processedFrames int)
	step         int
	lastReported int
}

func newProgressReporter(onProgress func(processedFrames int), step int) *progressReporter {
	if step <= 0 {
		step = 1
	}
	return &progressReporter{
		onProgress: onProgress,
		step:       step,
	}
}

func (p *progressReporter) Report(processedFrames int) {
	if p == nil || p.onProgress == nil {
		return
	}
	if processedFrames <= 0 {
		return
	}
	if processedFrames-p.lastReported < p.step && processedFrames != 1 {
		return
	}
	p.lastReported = processedFrames
	p.onProgress(processedFrames)
}

func RunWithProgress(cmd *exec.Cmd, onProgress func(currentFrame int), hooks *ProcessHooks) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open ffmpeg progress pipe: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := startWithHooks(cmd, hooks); err != nil {
		return err
	}
	defer finishWithHooks(cmd, hooks)

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "frame=") {
			frame, parseErr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "frame=")))
			if parseErr == nil && frame >= 0 {
				onProgress(frame)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read ffmpeg progress: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg exited with error: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func scaledSampleSize(width int, height int, maxSide int) (int, int) {
	if width <= 0 || height <= 0 {
		return 0, 0
	}
	if intMax(width, height) <= maxSide {
		return Even(width), Even(height)
	}
	if width >= height {
		scaledWidth := maxSide
		scaledHeight := int(float64(height) * float64(maxSide) / float64(width))
		return Even(scaledWidth), intMax(Even(scaledHeight), 2)
	}
	scaledHeight := maxSide
	scaledWidth := int(float64(width) * float64(maxSide) / float64(height))
	return intMax(Even(scaledWidth), 2), Even(scaledHeight)
}

func detectFrameBorders(frame []byte, width int, height int, opts BlackBorderOptions) (top int, bottom int, left int, right int, ok bool) {
	if len(frame) < width*height || width <= 0 || height <= 0 {
		return 0, 0, 0, 0, false
	}
	opts = normalizeBlackBorderOptions(opts)
	if opts.Mode == BlackBorderModeLegacy {
		return detectFrameBordersLegacy(frame, width, height)
	}
	return detectFrameBordersCenterCrop(frame, width, height, opts)
}

func detectFrameBordersCenterCrop(frame []byte, width int, height int, opts BlackBorderOptions) (top int, bottom int, left int, right int, ok bool) {
	pureBlackThreshold := byte(opts.LineThreshold)
	minBlackRatio := opts.LineRatio
	requiredBlackRun := opts.RequiredRun

	rowStart, rowEnd := centralBand(width)
	top = findBlackBoundaryFromCenterToStart(height, func(y int) float64 {
		return blackPixelRatioInRow(frame, width, y, rowStart, rowEnd, pureBlackThreshold)
	}, minBlackRatio, requiredBlackRun)
	bottom = findBlackBoundaryFromCenterToEnd(height, func(y int) float64 {
		return blackPixelRatioInRow(frame, width, y, rowStart, rowEnd, pureBlackThreshold)
	}, minBlackRatio, requiredBlackRun)

	colStart, colEnd := activeBand(height, top, bottom)
	left = findBlackBoundaryFromCenterToStart(width, func(x int) float64 {
		return blackPixelRatioInColumn(frame, width, height, x, colStart, colEnd, pureBlackThreshold)
	}, minBlackRatio, requiredBlackRun)
	right = findBlackBoundaryFromCenterToEnd(width, func(x int) float64 {
		return blackPixelRatioInColumn(frame, width, height, x, colStart, colEnd, pureBlackThreshold)
	}, minBlackRatio, requiredBlackRun)

	refinedRowStart, refinedRowEnd := activeBand(width, left, right)
	top = findBlackBoundaryFromCenterToStart(height, func(y int) float64 {
		return blackPixelRatioInRow(frame, width, y, refinedRowStart, refinedRowEnd, pureBlackThreshold)
	}, minBlackRatio, requiredBlackRun)
	bottom = findBlackBoundaryFromCenterToEnd(height, func(y int) float64 {
		return blackPixelRatioInRow(frame, width, y, refinedRowStart, refinedRowEnd, pureBlackThreshold)
	}, minBlackRatio, requiredBlackRun)

	refinedColStart, refinedColEnd := activeBand(height, top, bottom)
	left = findBlackBoundaryFromCenterToStart(width, func(x int) float64 {
		return blackPixelRatioInColumn(frame, width, height, x, refinedColStart, refinedColEnd, pureBlackThreshold)
	}, minBlackRatio, requiredBlackRun)
	right = findBlackBoundaryFromCenterToEnd(width, func(x int) float64 {
		return blackPixelRatioInColumn(frame, width, height, x, refinedColStart, refinedColEnd, pureBlackThreshold)
	}, minBlackRatio, requiredBlackRun)

	activeWidth := width - left - right
	activeHeight := height - top - bottom
	if activeWidth <= 0 || activeHeight <= 0 {
		return 0, 0, 0, 0, false
	}
	return top, bottom, left, right, true
}

func detectFrameBordersLegacy(frame []byte, width int, height int) (top int, bottom int, left int, right int, ok bool) {
	const (
		darkThreshold         = 34
		maxBrightRatio        = 0.03
		maxDarkAverageLuma    = 18.0
		minContentBrightRatio = 0.25
		minContentAverageLuma = 28.0
		contentRunLength      = 3
		minActiveDimension    = 0.45
	)

	rowMetrics := func(y int, xStart int, xEnd int) (brightRatio float64, averageLuma float64) {
		if xStart < 0 {
			xStart = 0
		}
		if xEnd > width {
			xEnd = width
		}
		if xEnd <= xStart {
			return 0, 0
		}
		offset := y * width
		bright := 0
		sum := 0
		for x := xStart; x < xEnd; x++ {
			value := int(frame[offset+x])
			sum += value
			if value > darkThreshold {
				bright++
			}
		}
		span := xEnd - xStart
		return float64(bright) / float64(span), float64(sum) / float64(span)
	}

	colMetrics := func(x int, yStart int, yEnd int) (brightRatio float64, averageLuma float64) {
		if yStart < 0 {
			yStart = 0
		}
		if yEnd > height {
			yEnd = height
		}
		if yEnd <= yStart {
			return 0, 0
		}
		bright := 0
		sum := 0
		for y := yStart; y < yEnd; y++ {
			value := int(frame[y*width+x])
			sum += value
			if value > darkThreshold {
				bright++
			}
		}
		span := yEnd - yStart
		return float64(bright) / float64(span), float64(sum) / float64(span)
	}

	top = findBorderFromStart(height, func(i int) edgeMetrics {
		brightRatio, averageLuma := rowMetrics(i, 0, width)
		return edgeMetrics{BrightRatio: brightRatio, AverageLuma: averageLuma}
	}, maxBrightRatio, maxDarkAverageLuma, minContentBrightRatio, minContentAverageLuma, contentRunLength)
	bottom = findBorderFromEnd(height, func(i int) edgeMetrics {
		brightRatio, averageLuma := rowMetrics(i, 0, width)
		return edgeMetrics{BrightRatio: brightRatio, AverageLuma: averageLuma}
	}, maxBrightRatio, maxDarkAverageLuma, minContentBrightRatio, minContentAverageLuma, contentRunLength)

	yStart := top
	yEnd := height - bottom
	if yStart < 0 {
		yStart = 0
	}
	if yEnd <= yStart {
		yStart = 0
		yEnd = height
	}

	left, right = findHorizontalBordersFromCenter(width, func(i int) edgeMetrics {
		brightRatio, averageLuma := colMetrics(i, yStart, yEnd)
		return edgeMetrics{BrightRatio: brightRatio, AverageLuma: averageLuma}
	}, maxBrightRatio, maxDarkAverageLuma, contentRunLength)

	activeWidth := width - left - right
	activeHeight := height - top - bottom
	if activeWidth <= 0 || activeHeight <= 0 {
		return 0, 0, 0, 0, false
	}
	if float64(activeWidth) < float64(width)*minActiveDimension || float64(activeHeight) < float64(height)*minActiveDimension {
		return 0, 0, 0, 0, false
	}
	return top, bottom, left, right, true
}

type edgeMetrics struct {
	BrightRatio float64
	AverageLuma float64
}

func findHorizontalBordersFromCenter(limit int, metrics func(index int) edgeMetrics, maxDarkRatio float64, maxDarkAverageLuma float64, contentRunLength int) (left int, right int) {
	seedStart, seedEnd, ok := findCenterContentSeed(limit, metrics, maxDarkRatio, maxDarkAverageLuma, contentRunLength)
	if !ok {
		return 0, 0
	}

	for i := seedStart - 1; i >= 0; i-- {
		if isStrictDark(metrics(i), maxDarkRatio, maxDarkAverageLuma) {
			left = i + 1
			break
		}
	}

	for i := seedEnd + 1; i < limit; i++ {
		if isStrictDark(metrics(i), maxDarkRatio, maxDarkAverageLuma) {
			right = limit - i
			break
		}
	}

	return left, right
}

func findCenterContentSeed(limit int, metrics func(index int) edgeMetrics, maxDarkRatio float64, maxDarkAverageLuma float64, contentRunLength int) (start int, end int, ok bool) {
	if limit <= 0 || contentRunLength <= 0 || contentRunLength > limit {
		return 0, 0, false
	}

	center := float64(limit-1) / 2.0
	bestStart := -1
	bestDistance := math.MaxFloat64

	for runStart := 0; runStart <= limit-contentRunLength; runStart++ {
		valid := true
		for offset := 0; offset < contentRunLength; offset++ {
			if isStrictDark(metrics(runStart+offset), maxDarkRatio, maxDarkAverageLuma) {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}

		runMid := float64(runStart+runStart+contentRunLength-1) / 2.0
		distance := math.Abs(runMid - center)
		if distance < bestDistance {
			bestDistance = distance
			bestStart = runStart
		}
	}

	if bestStart < 0 {
		return 0, 0, false
	}
	return bestStart, bestStart + contentRunLength - 1, true
}

func isStrictDark(value edgeMetrics, maxDarkRatio float64, maxDarkAverageLuma float64) bool {
	return value.BrightRatio <= maxDarkRatio && value.AverageLuma <= maxDarkAverageLuma
}

func findBorderFromStart(limit int, metrics func(index int) edgeMetrics, maxDarkRatio float64, maxDarkAverageLuma float64, minContentRatio float64, minContentAverageLuma float64, contentRunLength int) int {
	seenDarkEdge := false
	contentStart := -1
	contentRun := 0

	for i := 0; i < limit; i++ {
		value := metrics(i)
		if value.BrightRatio <= maxDarkRatio || value.AverageLuma <= maxDarkAverageLuma {
			seenDarkEdge = true
		}
		if value.BrightRatio >= minContentRatio || value.AverageLuma >= minContentAverageLuma {
			if contentStart == -1 {
				contentStart = i
			}
			contentRun++
			if contentRun >= contentRunLength {
				if seenDarkEdge && contentStart > 0 {
					return contentStart
				}
				return 0
			}
			continue
		}
		contentStart = -1
		contentRun = 0
	}

	return 0
}

func findBorderFromEnd(limit int, metrics func(index int) edgeMetrics, maxDarkRatio float64, maxDarkAverageLuma float64, minContentRatio float64, minContentAverageLuma float64, contentRunLength int) int {
	seenDarkEdge := false
	contentEnd := -1
	contentRun := 0

	for offset := 0; offset < limit; offset++ {
		i := limit - 1 - offset
		value := metrics(i)
		if value.BrightRatio <= maxDarkRatio || value.AverageLuma <= maxDarkAverageLuma {
			seenDarkEdge = true
		}
		if value.BrightRatio >= minContentRatio || value.AverageLuma >= minContentAverageLuma {
			if contentEnd == -1 {
				contentEnd = offset
			}
			contentRun++
			if contentRun >= contentRunLength {
				if seenDarkEdge && contentEnd > 0 {
					return contentEnd
				}
				return 0
			}
			continue
		}
		contentEnd = -1
		contentRun = 0
	}

	return 0
}

func blackPixelRatioInRow(frame []byte, width int, y int, xStart int, xEnd int, threshold byte) float64 {
	if width <= 0 {
		return 0
	}
	height := len(frame) / width
	if y < 0 || y >= height {
		return 0
	}
	if xStart < 0 {
		xStart = 0
	}
	if xEnd > width {
		xEnd = width
	}
	if xEnd <= xStart {
		return 0
	}

	black := 0
	offset := y * width
	for x := xStart; x < xEnd; x++ {
		if frame[offset+x] <= threshold {
			black++
		}
	}
	return float64(black) / float64(xEnd-xStart)
}

func normalizeBlackBorderOptions(opts BlackBorderOptions) BlackBorderOptions {
	switch strings.ToLower(strings.TrimSpace(opts.Mode)) {
	case "", BlackBorderModeCenterCrop:
		opts.Mode = BlackBorderModeCenterCrop
	case BlackBorderModeLegacy:
		opts.Mode = BlackBorderModeLegacy
	default:
		opts.Mode = BlackBorderModeCenterCrop
	}
	if opts.LineThreshold < 0 || opts.LineThreshold > 255 {
		opts.LineThreshold = 6
	}
	if opts.LineRatio <= 0 || opts.LineRatio > 1 {
		opts.LineRatio = 0.60
	}
	if opts.RequiredRun <= 0 {
		opts.RequiredRun = 2
	}
	if opts.SampleFPS <= 0 {
		opts.SampleFPS = 4
	}
	if opts.SampleDuration <= 0 {
		opts.SampleDuration = 1
	}
	if opts.SampleFrameCap <= 0 {
		opts.SampleFrameCap = 4
	}
	return opts
}

func blackPixelRatioInColumn(frame []byte, width int, height int, x int, yStart int, yEnd int, threshold byte) float64 {
	if x < 0 || x >= width {
		return 0
	}
	if yStart < 0 {
		yStart = 0
	}
	if yEnd > height {
		yEnd = height
	}
	if yEnd <= yStart {
		return 0
	}

	black := 0
	for y := yStart; y < yEnd; y++ {
		if frame[y*width+x] <= threshold {
			black++
		}
	}
	return float64(black) / float64(yEnd-yStart)
}

func findBlackBoundaryFromCenterToStart(limit int, blackRatio func(index int) float64, requiredRatio float64, requiredRun int) int {
	if limit <= 0 {
		return 0
	}
	if requiredRun <= 0 {
		requiredRun = 1
	}
	center := limit / 2
	run := 0
	for i := center; i >= 0; i-- {
		if blackRatio(i) >= requiredRatio {
			run++
			if run >= requiredRun {
				return i + requiredRun
			}
			continue
		}
		run = 0
	}
	return 0
}

func findBlackBoundaryFromCenterToEnd(limit int, blackRatio func(index int) float64, requiredRatio float64, requiredRun int) int {
	if limit <= 0 {
		return 0
	}
	if requiredRun <= 0 {
		requiredRun = 1
	}
	center := limit / 2
	run := 0
	for i := center; i < limit; i++ {
		if blackRatio(i) >= requiredRatio {
			run++
			if run >= requiredRun {
				innerMost := i - requiredRun + 1
				return limit - innerMost
			}
			continue
		}
		run = 0
	}
	return 0
}

func centralBand(limit int) (start int, end int) {
	if limit <= 0 {
		return 0, 0
	}
	start = limit / 4
	end = limit - start
	if end <= start {
		return 0, limit
	}
	return start, end
}

func activeBand(limit int, leading int, trailing int) (start int, end int) {
	start = leading
	end = limit - trailing
	if start < 0 {
		start = 0
	}
	if end > limit {
		end = limit
	}
	if end <= start {
		return centralBand(limit)
	}
	return start, end
}

func averageInt(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sum := 0
	for _, value := range values {
		sum += value
	}
	return int(math.Round(float64(sum) / float64(len(values))))
}

func medianInt(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	return sorted[len(sorted)/2]
}

func scaleCropRect(sampleWidth int, sampleHeight int, sourceWidth int, sourceHeight int, left int, right int, top int, bottom int) CropRect {
	if sampleWidth <= 0 || sampleHeight <= 0 || sourceWidth <= 0 || sourceHeight <= 0 {
		return CropRect{}
	}

	left = clampInt(left, 0, sampleWidth)
	right = clampInt(right, 0, sampleWidth-left)
	top = clampInt(top, 0, sampleHeight)
	bottom = clampInt(bottom, 0, sampleHeight-top)

	scaleX := float64(sourceWidth) / float64(sampleWidth)
	scaleY := float64(sourceHeight) / float64(sampleHeight)

	x := evenCeil(float64(left) * scaleX)
	y := evenCeil(float64(top) * scaleY)
	endX := evenFloor(float64(sampleWidth-right) * scaleX)
	endY := evenFloor(float64(sampleHeight-bottom) * scaleY)

	if left == 0 {
		x = 0
	}
	if top == 0 {
		y = 0
	}
	if right == 0 {
		endX = Even(sourceWidth)
	}
	if bottom == 0 {
		endY = Even(sourceHeight)
	}

	width := endX - x
	height := endY - y
	if width <= 0 || height <= 0 {
		return CropRect{}
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x+width > Even(sourceWidth) {
		width = Even(sourceWidth - x)
	}
	if y+height > Even(sourceHeight) {
		height = Even(sourceHeight - y)
	}
	if width <= 0 || height <= 0 {
		return CropRect{}
	}
	if x == 0 && y == 0 && width == Even(sourceWidth) && height == Even(sourceHeight) {
		return CropRect{}
	}
	return CropRect{X: x, Y: y, Width: width, Height: height}
}

func scaleCropRectLegacy(sampleWidth int, sampleHeight int, sourceWidth int, sourceHeight int, left int, right int, top int, bottom int) CropRect {
	left, right = pairedMargins(left, right)
	top, bottom = pairedMargins(top, bottom)

	scaleX := float64(sourceWidth) / float64(sampleWidth)
	scaleY := float64(sourceHeight) / float64(sampleHeight)

	x := Even(int(math.Round(float64(left) * scaleX)))
	y := Even(int(math.Round(float64(top) * scaleY)))
	rightPx := Even(int(math.Round(float64(right) * scaleX)))
	bottomPx := Even(int(math.Round(float64(bottom) * scaleY)))

	width := Even(sourceWidth - x - rightPx)
	height := Even(sourceHeight - y - bottomPx)
	if width <= 0 || height <= 0 {
		return CropRect{}
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x+width > sourceWidth {
		width = Even(sourceWidth - x)
	}
	if y+height > sourceHeight {
		height = Even(sourceHeight - y)
	}
	if width <= 0 || height <= 0 {
		return CropRect{}
	}
	if x == 0 && y == 0 && width == Even(sourceWidth) && height == Even(sourceHeight) {
		return CropRect{}
	}
	return CropRect{X: x, Y: y, Width: width, Height: height}
}

func intMax(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func evenFloor(value float64) int {
	return Even(int(math.Floor(value)))
}

func evenCeil(value float64) int {
	n := int(math.Ceil(value))
	if n%2 != 0 {
		n++
	}
	return n
}

func clampInt(value int, min int, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func pairedMargins(a int, b int) (int, int) {
	if a <= 0 || b <= 0 {
		return 0, 0
	}
	paired := minInt(a, b)
	return paired, paired
}

func updateColumnBlackCountsLegacy(frame []byte, width int, height int, blackCounts []int, maxBrightRatio float64, maxAverageLuma float64) {
	for x := 0; x < width; x++ {
		bright := 0
		sum := 0
		for y := 0; y < height; y++ {
			value := int(frame[y*width+x])
			sum += value
			if value > 34 {
				bright++
			}
		}
		brightRatio := float64(bright) / float64(height)
		averageLuma := float64(sum) / float64(height)
		if brightRatio <= maxBrightRatio && averageLuma <= maxAverageLuma {
			blackCounts[x]++
		}
	}
}

func persistentEdgeSpanFromStartLegacy(counts []int, totalFrames int, requiredFraction float64) int {
	required := int(math.Ceil(float64(totalFrames) * requiredFraction))
	span := 0
	for _, count := range counts {
		if count < required {
			break
		}
		span++
	}
	return span
}

func persistentEdgeSpanFromEndLegacy(counts []int, totalFrames int, requiredFraction float64) int {
	required := int(math.Ceil(float64(totalFrames) * requiredFraction))
	span := 0
	for i := len(counts) - 1; i >= 0; i-- {
		if counts[i] < required {
			break
		}
		span++
	}
	return span
}

func startWithHooks(cmd *exec.Cmd, hooks *ProcessHooks) error {
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", filepath.Base(cmd.Path), err)
	}
	if hooks != nil && hooks.Started != nil {
		if err := hooks.Started(cmd.Process); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			finishWithHooks(cmd, hooks)
			return err
		}
	}
	return nil
}

func finishWithHooks(cmd *exec.Cmd, hooks *ProcessHooks) {
	if hooks != nil && hooks.Finished != nil && cmd != nil {
		hooks.Finished(cmd.Process)
	}
}

func FindVideos(root string) ([]string, error) {
	exts := map[string]bool{
		".mp4":  true,
		".mov":  true,
		".mkv":  true,
		".avi":  true,
		".flv":  true,
		".wmv":  true,
		".m4v":  true,
		".webm": true,
	}

	var videos []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if exts[ext] {
			videos = append(videos, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(videos)
	return videos, nil
}

func fromDiagnostics(root string) (Binaries, error) {
	var candidates []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Base(path), "ffmpeg_full_diagnostics.json") {
			candidates = append(candidates, path)
		}
		return nil
	})
	if err != nil {
		return Binaries{}, err
	}

	for _, candidate := range candidates {
		raw, readErr := os.ReadFile(candidate)
		if readErr != nil {
			continue
		}

		var payload diagnostics
		if jsonErr := json.Unmarshal(raw, &payload); jsonErr != nil {
			continue
		}

		ffmpegPath := payload.Components["ffmpeg"].Path
		ffprobePath := payload.Components["ffprobe"].Path
		if fileExists(ffmpegPath) && fileExists(ffprobePath) {
			return Binaries{
				FFmpeg:  ffmpegPath,
				FFprobe: ffprobePath,
				Source:  candidate,
			}, nil
		}
	}

	return Binaries{}, errors.New("no usable diagnostics file found")
}

func fromFilesystem(root string) (Binaries, error) {
	if strings.TrimSpace(root) == "" {
		return Binaries{}, errors.New("workspace scan root is empty")
	}
	wantFFmpeg := binaryName("ffmpeg")
	wantFFprobe := binaryName("ffprobe")

	var bins Binaries
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}

		name := filepath.Base(path)
		switch {
		case bins.FFmpeg == "" && strings.EqualFold(name, wantFFmpeg):
			bins.FFmpeg = path
		case bins.FFprobe == "" && strings.EqualFold(name, wantFFprobe):
			bins.FFprobe = path
		}

		if bins.FFmpeg != "" && bins.FFprobe != "" {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return Binaries{}, err
	}
	if bins.FFmpeg == "" || bins.FFprobe == "" {
		return Binaries{}, errors.New("ffmpeg binaries not found in workspace scan")
	}
	bins.Source = root
	return bins, nil
}

func fromExecutableDir() (Binaries, error) {
	executablePath, err := executablePathFunc()
	if err != nil {
		return Binaries{}, err
	}

	executableDir := filepath.Dir(executablePath)
	return fromCandidateDirectories([]candidateDir{
		{Path: filepath.Join(executableDir, "ffmpeg", "bin"), Source: filepath.Join(executableDir, "ffmpeg", "bin")},
		{Path: filepath.Join(executableDir, "ffmpeg"), Source: filepath.Join(executableDir, "ffmpeg")},
		{Path: executableDir, Source: executableDir},
	})
}

func fromDriveRoots() (Binaries, error) {
	roots, err := driveRootsFunc()
	if err != nil {
		return Binaries{}, err
	}

	var candidates []candidateDir
	for _, root := range roots {
		candidates = append(candidates,
			candidateDir{
				Path:   filepath.Join(root, "ffmpeg", "bin"),
				Source: filepath.Join(root, "ffmpeg", "bin"),
			},
			candidateDir{
				Path:   filepath.Join(root, "ffmpeg"),
				Source: filepath.Join(root, "ffmpeg"),
			},
		)
	}

	return fromCandidateDirectories(candidates)
}

func fromPath() (Binaries, error) {
	ffmpegPath, err := lookPathFunc(binaryName("ffmpeg"))
	if err != nil {
		return Binaries{}, err
	}
	ffprobePath, err := lookPathFunc(binaryName("ffprobe"))
	if err != nil {
		return Binaries{}, err
	}

	return Binaries{
		FFmpeg:  ffmpegPath,
		FFprobe: ffprobePath,
		Source:  "PATH",
	}, nil
}

func fromEmbedded(root string) (Binaries, error) {
	payloads, ok := embeddedPayloads()
	if !ok {
		return Binaries{}, errors.New("no embedded ffmpeg payloads available")
	}

	runtimeDir, err := chooseRuntimeDir(root)
	if err != nil {
		return Binaries{}, err
	}
	binDir := filepath.Join(runtimeDir, "embedded-ffmpeg", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return Binaries{}, fmt.Errorf("create embedded ffmpeg directory: %w", err)
	}

	if err := writeEmbeddedRuntime(binDir, payloads); err != nil {
		return Binaries{}, err
	}

	ffmpegPath := filepath.Join(binDir, binaryName("ffmpeg"))
	ffprobePath := filepath.Join(binDir, binaryName("ffprobe"))
	if !fileExists(ffmpegPath) || !fileExists(ffprobePath) {
		return Binaries{}, errors.New("embedded runtime missing ffmpeg.exe or ffprobe.exe")
	}

	return Binaries{
		FFmpeg:  ffmpegPath,
		FFprobe: ffprobePath,
		Source:  "embedded",
	}, nil
}

type candidateDir struct {
	Path   string
	Source string
}

func fromCandidateDirectories(candidates []candidateDir) (Binaries, error) {
	for _, candidate := range candidates {
		bins := binariesFromDirectory(candidate.Path, candidate.Source)
		if bins.FFmpeg != "" && bins.FFprobe != "" {
			return bins, nil
		}
	}
	return Binaries{}, errors.New("ffmpeg binaries not found in candidate directories")
}

func binariesFromDirectory(dir string, source string) Binaries {
	if strings.TrimSpace(dir) == "" {
		return Binaries{}
	}

	ffmpegPath := filepath.Join(dir, binaryName("ffmpeg"))
	ffprobePath := filepath.Join(dir, binaryName("ffprobe"))
	if fileExists(ffmpegPath) && fileExists(ffprobePath) {
		return Binaries{
			FFmpeg:  ffmpegPath,
			FFprobe: ffprobePath,
			Source:  source,
		}
	}
	return Binaries{}
}

func binaryName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func firstPositiveInt(values ...string) int {
	for _, value := range values {
		if value == "" || strings.EqualFold(value, "N/A") {
			continue
		}
		n, err := strconv.Atoi(value)
		if err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func mergeEnvOverrides(overrides Binaries) Binaries {
	if overrides.FFmpeg == "" {
		overrides.FFmpeg = firstNonEmpty(os.Getenv("ONEKEYVE_FFMPEG"), os.Getenv("FFMPEG_PATH"))
	}
	if overrides.FFprobe == "" {
		overrides.FFprobe = firstNonEmpty(os.Getenv("ONEKEYVE_FFPROBE"), os.Getenv("FFPROBE_PATH"))
	}
	return overrides
}

func applyOverrides(bins Binaries, overrides Binaries) Binaries {
	usedOverride := false
	if overrides.FFmpeg != "" {
		bins.FFmpeg = overrides.FFmpeg
		usedOverride = true
	}
	if overrides.FFprobe != "" {
		bins.FFprobe = overrides.FFprobe
		usedOverride = true
	}
	if usedOverride && bins.Source != "" && bins.Source != "flags/env" {
		bins.Source += " + flags/env"
	}
	return bins
}

func listDriveRoots() ([]string, error) {
	if runtime.GOOS != "windows" {
		return []string{string(filepath.Separator)}, nil
	}

	var roots []string
	for drive := 'A'; drive <= 'Z'; drive++ {
		root := fmt.Sprintf("%c:\\", drive)
		info, err := os.Stat(root)
		if err == nil && info.IsDir() {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		return nil, errors.New("no drive roots found")
	}
	return roots, nil
}

func chooseRuntimeDir(root string) (string, error) {
	var failures []string
	for _, candidate := range runtimeBaseCandidates(root) {
		if candidate == "" {
			continue
		}
		if isForbiddenOutputPath(candidate) {
			continue
		}
		runtimeDir := filepath.Join(candidate, "OneKeyVE")
		if err := prepareRuntimeDir(runtimeDir); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", runtimeDir, err))
			continue
		}
		return runtimeDir, nil
	}
	if len(failures) > 0 {
		return "", fmt.Errorf("no writable non-C runtime directory available for embedded ffmpeg (%s)", strings.Join(failures, "; "))
	}
	return "", errors.New("no writable non-C runtime directory available for embedded ffmpeg")
}

func runtimeBaseCandidates(root string) []string {
	candidates := []string{
		os.Getenv("ONEKEYVE_RUNTIME_DIR"),
		os.Getenv("ONEKEYVE_CACHE_DIR"),
		os.Getenv("TMPDIR"),
		os.Getenv("TEMP"),
		os.Getenv("TMP"),
	}

	if executablePath, err := executablePathFunc(); err == nil {
		candidates = append(candidates, filepath.Dir(executablePath))
	}
	if filepath.IsAbs(root) {
		candidates = append(candidates, root)
	}
	return candidates
}

func writePayloadIfDifferent(path string, payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("embedded payload missing for %s", path)
	}

	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, payload) {
		return nil
	}
	if err := os.WriteFile(path, payload, 0o755); err != nil {
		return fmt.Errorf("write embedded payload %s: %w", path, err)
	}
	return nil
}

func writeEmbeddedRuntime(binDir string, payloads map[string][]byte) error {
	for name, payload := range payloads {
		if err := writePayloadIfDifferent(filepath.Join(binDir, name), payload); err != nil {
			return err
		}
	}
	return nil
}

func isForbiddenOutputPath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return true
	}
	return strings.EqualFold(filepath.VolumeName(clean), "C:")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func validateOverrides(overrides Binaries) error {
	if err := validateOverrideBinary("ffmpeg", overrides.FFmpeg); err != nil {
		return err
	}
	if err := validateOverrideBinary("ffprobe", overrides.FFprobe); err != nil {
		return err
	}
	return nil
}

func validateOverrideBinary(name string, path string) error {
	if path == "" {
		return nil
	}
	if fileExists(path) {
		return nil
	}
	return fmt.Errorf("%s override does not exist or is not a file: %s", name, path)
}

func ensureRuntimeDirWritable(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}

	handle, err := os.CreateTemp(path, ".runtime-check-*")
	if err != nil {
		return err
	}
	name := handle.Name()
	if closeErr := handle.Close(); closeErr != nil {
		_ = os.Remove(name)
		return closeErr
	}
	if err := os.Remove(name); err != nil {
		return err
	}
	return nil
}
