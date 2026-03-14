package app

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"onekeyvego/internal/ffmpeg"
	"onekeyvego/internal/procutil"
)

type Target struct {
	Label string
	Ratio float64
}

type Config struct {
	WorkDir               string
	OutputDir             string
	FFmpegPath            string
	FFprobePath           string
	Encoder               string
	BlackBorderMode       string
	BlurSigma             int
	FeatherPx             int
	BlackLineThreshold    int
	BlackLineRatioPercent int
	BlackLineRequiredRun  int
	Targets               []Target
	Controller            *RunController
	OnLog                 func(string)
	OnProgress            func(ProgressUpdate)
}

type ProgressUpdate struct {
	TotalTasks     int
	CompletedTasks int
	CurrentTask    int
	VideoName      string
	TargetLabel    string
	CurrentFrame   int
	TotalFrames    int
	Percent        float64
}

type targetRenderPlan struct {
	Target                Target
	OutputDir             string
	OutputPath            string
	SkipExisting          bool
	RemovedFailedExisting bool
}

func DefaultConfig(workdir string) Config {
	return Config{
		WorkDir:               workdir,
		OutputDir:             filepath.Join(workdir, "output"),
		Encoder:               "h264_nvenc",
		BlackBorderMode:       ffmpeg.BlackBorderModeCenterCrop,
		BlurSigma:             20,
		FeatherPx:             30,
		BlackLineThreshold:    6,
		BlackLineRatioPercent: 60,
		BlackLineRequiredRun:  2,
		Targets: []Target{
			{Label: "9x20", Ratio: 9.0 / 20.0},
			{Label: "5x11", Ratio: 5.0 / 11.0},
		},
	}
}

func emitLog(cfg Config, format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	log.Print(line)
	if cfg.OnLog != nil {
		cfg.OnLog(line)
	}
}

func emitProgress(cfg Config, update ProgressUpdate) {
	if update.TotalTasks < 0 {
		update.TotalTasks = 0
	}
	if update.CompletedTasks < 0 {
		update.CompletedTasks = 0
	}
	if update.CurrentTask < 0 {
		update.CurrentTask = 0
	}
	if update.Percent < 0 {
		update.Percent = 0
	}
	if update.Percent > 100 {
		update.Percent = 100
	}
	if cfg.OnProgress != nil {
		cfg.OnProgress(update)
	}
}

func Run(cfg Config) error {
	if err := waitForControl(cfg); err != nil {
		return err
	}
	if cfg.BlurSigma <= 0 {
		return fmt.Errorf("blur must be positive")
	}
	if cfg.FeatherPx <= 0 {
		return fmt.Errorf("feather must be positive")
	}
	if cfg.BlackLineThreshold < 0 || cfg.BlackLineThreshold > 255 {
		return fmt.Errorf("black line threshold must be between 0 and 255")
	}
	if cfg.BlackBorderMode != ffmpeg.BlackBorderModeCenterCrop && cfg.BlackBorderMode != ffmpeg.BlackBorderModeLegacy {
		return fmt.Errorf("black border mode must be %q or %q", ffmpeg.BlackBorderModeCenterCrop, ffmpeg.BlackBorderModeLegacy)
	}
	if cfg.BlackLineRatioPercent <= 0 || cfg.BlackLineRatioPercent > 100 {
		return fmt.Errorf("black line ratio percent must be between 1 and 100")
	}
	if cfg.BlackLineRequiredRun <= 0 {
		return fmt.Errorf("black line required run must be positive")
	}
	if len(cfg.Targets) == 0 {
		return fmt.Errorf("at least one target ratio is required")
	}
	outputRoot := resolveOutputDir(cfg.WorkDir, cfg.OutputDir)
	if isCDrivePath(outputRoot) {
		return fmt.Errorf("refusing to write output under C drive: %s", outputRoot)
	}
	cfg.OutputDir = outputRoot

	bins, err := ffmpeg.Locate(cfg.WorkDir, ffmpeg.Binaries{
		FFmpeg:  cfg.FFmpegPath,
		FFprobe: cfg.FFprobePath,
	})
	if err != nil {
		return err
	}

	hooks := processHooks(cfg)
	encoder := ffmpeg.DetectPreferredEncoder(bins.FFmpeg, cfg.Encoder, hooks)
	emitLog(cfg, "ffmpeg: %s", bins.FFmpeg)
	emitLog(cfg, "ffprobe: %s", bins.FFprobe)
	emitLog(cfg, "binary source: %s", bins.Source)
	emitLog(cfg, "video encoder: %s", encoder)
	emitLog(cfg, "black border mode: %s", cfg.BlackBorderMode)
	emitLog(cfg, "black border detect: first-second avg of 4 frames, threshold<=%d, black-ratio>=%d%%, min-run=%d", cfg.BlackLineThreshold, cfg.BlackLineRatioPercent, cfg.BlackLineRequiredRun)

	videos, err := DiscoverVideos(cfg)
	if err != nil {
		return fmt.Errorf("discover videos: %w", err)
	}
	if len(videos) == 0 {
		return fmt.Errorf("no video files found in %s", cfg.WorkDir)
	}

	totalTasks := len(videos) * len(cfg.Targets)
	emitLog(cfg, "found %d video(s) in %s", len(videos), cfg.WorkDir)
	emitProgress(cfg, ProgressUpdate{TotalTasks: totalTasks, Percent: 0})

	completedTasks := 0
	for _, videoPath := range videos {
		if err := waitForControl(cfg); err != nil {
			return err
		}
		if err := processVideo(cfg, bins, encoder, videoPath, completedTasks, totalTasks); err != nil {
			return err
		}
		completedTasks += len(cfg.Targets)
	}
	emitProgress(cfg, ProgressUpdate{
		TotalTasks:     totalTasks,
		CompletedTasks: totalTasks,
		CurrentTask:    totalTasks,
		Percent:        100,
	})
	return nil
}

func processVideo(cfg Config, bins ffmpeg.Binaries, encoder string, videoPath string, completedBase int, totalTasks int) error {
	if err := waitForControl(cfg); err != nil {
		return err
	}
	plans, err := planTargetRenders(cfg, videoPath)
	if err != nil {
		return err
	}

	videoName := filepath.Base(videoPath)
	firstRenderableIndex := -1
	for index, plan := range plans {
		taskIndex := completedBase + index
		if plan.SkipExisting {
			emitLog(cfg, "skip existing render [%s] %s -> %s", plan.Target.Label, videoName, plan.OutputPath)
			emitProgress(cfg, ProgressUpdate{
				TotalTasks:     totalTasks,
				CompletedTasks: taskIndex + 1,
				CurrentTask:    taskIndex + 1,
				VideoName:      videoName,
				TargetLabel:    plan.Target.Label,
				Percent:        percentForCompletedTasks(taskIndex+1, totalTasks),
			})
			continue
		}
		if plan.RemovedFailedExisting {
			emitLog(cfg, "existing output smaller than source, deleted and will re-render [%s] %s", plan.Target.Label, plan.OutputPath)
		}
		if firstRenderableIndex < 0 {
			firstRenderableIndex = index
		}
	}
	if firstRenderableIndex < 0 {
		emitLog(cfg, "all target renders already valid for %s, skipping source video", videoName)
		return nil
	}
	hooks := processHooks(cfg)
	meta, err := ffmpeg.Probe(bins.FFprobe, videoPath, hooks)
	if err != nil {
		if cfg.Controller != nil && cfg.Controller.StopRequested() {
			return ErrStopped
		}
		return err
	}

	crop := ffmpeg.CropRect{}
	completedBeforeAnalysis := completedBase + firstRenderableIndex
	emitLog(cfg, "analyzing black borders for %s", videoName)
	emitProgress(cfg, ProgressUpdate{
		TotalTasks:     totalTasks,
		CompletedTasks: completedBeforeAnalysis,
		CurrentTask:    completedBeforeAnalysis + 1,
		VideoName:      videoName,
		TargetLabel:    "黑边分析",
		TotalFrames:    meta.Frames,
		Percent:        percentForAnalysis(completedBeforeAnalysis, totalTasks, 0, meta.Frames),
	})
	detection, detectErr := ffmpeg.DetectBlackBorders(bins.FFmpeg, videoPath, meta.Width, meta.Height, ffmpeg.BlackBorderOptions{
		Mode:           cfg.BlackBorderMode,
		LineThreshold:  cfg.BlackLineThreshold,
		LineRatio:      float64(cfg.BlackLineRatioPercent) / 100.0,
		RequiredRun:    cfg.BlackLineRequiredRun,
		SampleFPS:      4,
		SampleDuration: 1,
		SampleFrameCap: 4,
	}, hooks, func(processedFrames int) {
		emitProgress(cfg, ProgressUpdate{
			TotalTasks:     totalTasks,
			CompletedTasks: completedBeforeAnalysis,
			CurrentTask:    completedBeforeAnalysis + 1,
			VideoName:      videoName,
			TargetLabel:    "黑边分析",
			CurrentFrame:   processedFrames,
			TotalFrames:    meta.Frames,
			Percent:        percentForAnalysis(completedBeforeAnalysis, totalTasks, processedFrames, meta.Frames),
		})
	})
	if errors.Is(detectErr, ErrStopped) || (cfg.Controller != nil && cfg.Controller.StopRequested()) {
		return ErrStopped
	}
	if detectErr != nil {
		emitLog(cfg, "black border detection skipped for %s: %v", videoName, detectErr)
	} else if detection.Rect.HasCrop() {
		crop = detection.Rect
		emitLog(cfg, "black border crop detected for %s: crop=%dx%d:%d:%d (samples=%d)", videoName, crop.Width, crop.Height, crop.X, crop.Y, detection.Frames)
	}
	emitProgress(cfg, ProgressUpdate{
		TotalTasks:     totalTasks,
		CompletedTasks: completedBeforeAnalysis,
		CurrentTask:    completedBeforeAnalysis + 1,
		VideoName:      videoName,
		TargetLabel:    "黑边分析",
		CurrentFrame:   meta.Frames,
		TotalFrames:    meta.Frames,
		Percent:        percentForAnalysis(completedBeforeAnalysis, totalTasks, meta.Frames, meta.Frames),
	})

	activeWidth := crop.ActiveWidth(meta.Width)
	activeHeight := crop.ActiveHeight(meta.Height)
	rotate, width, height := ffmpeg.PlannedDimensions(activeWidth, activeHeight)
	emitLog(cfg, "processing %s (source=%dx%d active=%dx%d rotate=%v normalized=%dx%d)", videoName, meta.Width, meta.Height, activeWidth, activeHeight, rotate, width, height)
	targetBitrate := targetVideoBitrate(meta.Bitrate)
	if targetBitrate > 0 {
		emitLog(cfg, "video bitrate plan for %s: source=%s target=%s", videoName, formatBitrate(meta.Bitrate), formatBitrate(targetBitrate))
	} else {
		emitLog(cfg, "video bitrate plan for %s: source bitrate unavailable, using encoder defaults", videoName)
	}

	for index, plan := range plans {
		if plan.SkipExisting {
			continue
		}
		if err := waitForControl(cfg); err != nil {
			return err
		}
		taskIndex := completedBase + index
		targetHeight := int(float64(width) / plan.Target.Ratio)
		filter := ffmpeg.BuildFilter(rotate, width, height, crop, targetHeight, cfg.BlurSigma, cfg.FeatherPx)
		emitProgress(cfg, ProgressUpdate{
			TotalTasks:     totalTasks,
			CompletedTasks: taskIndex,
			CurrentTask:    taskIndex + 1,
			VideoName:      videoName,
			TargetLabel:    plan.Target.Label,
			TotalFrames:    meta.Frames,
			Percent:        percentForTask(taskIndex, totalTasks, 0, meta.Frames, index == firstRenderableIndex),
		})

		if err := os.MkdirAll(plan.OutputDir, 0o755); err != nil {
			return fmt.Errorf("create output directory %s: %w", plan.OutputDir, err)
		}

		cmd := buildCommand(bins.FFmpeg, encoder, videoPath, plan.OutputPath, filter, targetBitrate)
		emitLog(cfg, "rendering [%s] %s -> %s", plan.Target.Label, videoName, plan.OutputPath)

		lastFrame := 0
		err := ffmpeg.RunWithProgress(cmd, func(frame int) {
			emitProgress(cfg, ProgressUpdate{
				TotalTasks:     totalTasks,
				CompletedTasks: taskIndex,
				CurrentTask:    taskIndex + 1,
				VideoName:      videoName,
				TargetLabel:    plan.Target.Label,
				CurrentFrame:   frame,
				TotalFrames:    meta.Frames,
				Percent:        percentForTask(taskIndex, totalTasks, frame, meta.Frames, index == firstRenderableIndex),
			})

			if meta.Frames > 0 {
				if frame-lastFrame >= 120 || frame == meta.Frames {
					progress := float64(frame) / float64(meta.Frames) * 100
					emitLog(cfg, "[%s] %s frame=%d/%d %.1f%%", plan.Target.Label, videoName, frame, meta.Frames, progress)
					lastFrame = frame
				}
				return
			}

			if frame-lastFrame >= 120 {
				emitLog(cfg, "[%s] %s frame=%d", plan.Target.Label, videoName, frame)
				lastFrame = frame
			}
		}, hooks)
		if err != nil {
			if cfg.Controller != nil && cfg.Controller.StopRequested() {
				return ErrStopped
			}
			return fmt.Errorf("render %s for %s: %w", plan.Target.Label, filepath.Base(videoPath), err)
		}
		emitProgress(cfg, ProgressUpdate{
			TotalTasks:     totalTasks,
			CompletedTasks: taskIndex + 1,
			CurrentTask:    taskIndex + 1,
			VideoName:      videoName,
			TargetLabel:    plan.Target.Label,
			CurrentFrame:   meta.Frames,
			TotalFrames:    meta.Frames,
			Percent:        percentForCompletedTasks(taskIndex+1, totalTasks),
		})
	}

	return nil
}

func waitForControl(cfg Config) error {
	if cfg.Controller == nil {
		return nil
	}
	return cfg.Controller.WaitIfPaused()
}

func processHooks(cfg Config) *ffmpeg.ProcessHooks {
	if cfg.Controller == nil {
		return nil
	}
	return &ffmpeg.ProcessHooks{
		Started:  cfg.Controller.AttachProcess,
		Finished: cfg.Controller.DetachProcess,
	}
}

func percentForTask(completedTasks int, totalTasks int, currentFrame int, totalFrames int, analysisReserved bool) float64 {
	if totalTasks <= 0 {
		return 0
	}
	progressUnits := float64(completedTasks)
	baseWithinTask := 0.0
	renderWeight := 1.0
	if analysisReserved {
		baseWithinTask = 0.15
		renderWeight = 0.85
	}
	progressUnits += baseWithinTask
	if totalFrames > 0 && currentFrame > 0 {
		framePortion := float64(currentFrame) / float64(totalFrames)
		if framePortion > 1 {
			framePortion = 1
		}
		progressUnits += framePortion * renderWeight
	}
	return progressUnits / float64(totalTasks) * 100
}

func percentForAnalysis(completedTasks int, totalTasks int, currentFrame int, totalFrames int) float64 {
	if totalTasks <= 0 {
		return 0
	}
	progressUnits := float64(completedTasks)
	if totalFrames > 0 && currentFrame > 0 {
		framePortion := float64(currentFrame) / float64(totalFrames)
		if framePortion > 1 {
			framePortion = 1
		}
		progressUnits += framePortion * 0.15
	}
	return progressUnits / float64(totalTasks) * 100
}

func percentForCompletedTasks(completedTasks int, totalTasks int) float64 {
	if totalTasks <= 0 {
		return 0
	}
	return float64(completedTasks) / float64(totalTasks) * 100
}

func buildCommand(ffmpegPath string, encoder string, inputPath string, outputPath string, filter string, bitrate int64) *exec.Cmd {
	args := []string{
		"-y",
		"-progress", "pipe:1",
		"-loglevel", "error",
		"-i", inputPath,
		"-filter_complex", filter,
		"-map", "[outv]",
	}

	switch strings.ToLower(encoder) {
	case "h264_nvenc":
		args = append(args,
			"-c:v", "h264_nvenc",
			"-rc:v", "vbr",
			"-preset", "p4",
			"-tune", "hq",
		)
	default:
		args = append(args,
			"-c:v", "libx264",
			"-preset", "medium",
			"-pix_fmt", "yuv420p",
		)
	}

	if bitrate > 0 {
		args = append(args,
			"-b:v", strconv.FormatInt(bitrate, 10),
			"-maxrate:v", strconv.FormatInt(bitrate, 10),
			"-bufsize:v", strconv.FormatInt(bitrate*2, 10),
		)
	} else {
		switch strings.ToLower(encoder) {
		case "h264_nvenc":
			args = append(args,
				"-cq:v", "24",
				"-b:v", "10M",
				"-maxrate:v", "15M",
				"-bufsize:v", "20M",
			)
		default:
			args = append(args,
				"-crf", "20",
				"-maxrate:v", "15M",
				"-bufsize:v", "20M",
			)
		}
	}

	args = append(args,
		"-map", "0:a?",
		"-c:a", "copy",
		outputPath,
	)

	cmd := exec.Command(ffmpegPath, args...)
	procutil.HideWindow(cmd)
	return cmd
}

func targetVideoBitrate(sourceBitrate int64) int64 {
	if sourceBitrate <= 0 {
		return 0
	}
	return sourceBitrate * 3 / 2
}

func formatBitrate(bitrate int64) string {
	if bitrate <= 0 {
		return "unknown"
	}
	mbps := float64(bitrate) / 1_000_000
	return fmt.Sprintf("%.2fMbps", mbps)
}

func isCDrivePath(path string) bool {
	if path == "" {
		return false
	}
	volume := strings.ToUpper(filepath.VolumeName(filepath.Clean(path)))
	return volume == "C:"
}

func resolveOutputDir(workDir string, outputDir string) string {
	outputRoot := filepath.Clean(outputDir)
	if filepath.IsAbs(outputRoot) {
		return outputRoot
	}
	return filepath.Join(workDir, outputRoot)
}

func planTargetRenders(cfg Config, videoPath string) ([]targetRenderPlan, error) {
	sourceInfo, err := os.Stat(videoPath)
	if err != nil {
		return nil, fmt.Errorf("stat source video %s: %w", videoPath, err)
	}
	if sourceInfo.IsDir() {
		return nil, fmt.Errorf("source video path is a directory: %s", videoPath)
	}

	videoName := filepath.Base(videoPath)
	sourceSize := sourceInfo.Size()
	plans := make([]targetRenderPlan, 0, len(cfg.Targets))
	for _, target := range cfg.Targets {
		outputDir := resolveVideoOutputDir(cfg, videoPath, target.Label)
		outputPath := filepath.Join(outputDir, videoName)
		plan := targetRenderPlan{
			Target:     target,
			OutputDir:  outputDir,
			OutputPath: outputPath,
		}

		outputInfo, err := os.Stat(outputPath)
		switch {
		case err == nil:
			if outputInfo.IsDir() {
				return nil, fmt.Errorf("output path is a directory: %s", outputPath)
			}
			if outputInfo.Size() >= sourceSize {
				plan.SkipExisting = true
			} else {
				if removeErr := os.Remove(outputPath); removeErr != nil {
					return nil, fmt.Errorf("remove failed output %s: %w", outputPath, removeErr)
				}
				plan.RemovedFailedExisting = true
			}
		case os.IsNotExist(err):
		default:
			return nil, fmt.Errorf("stat output %s: %w", outputPath, err)
		}

		plans = append(plans, plan)
	}
	return plans, nil
}

func DiscoverVideos(cfg Config) ([]string, error) {
	videos, err := ffmpeg.FindVideos(cfg.WorkDir)
	if err != nil {
		return nil, err
	}

	workRoot := filepath.Clean(cfg.WorkDir)
	outputRoot := resolveOutputDir(cfg.WorkDir, cfg.OutputDir)
	filtered := make([]string, 0, len(videos))
	for _, videoPath := range videos {
		if shouldSkipDiscoveredVideo(videoPath, workRoot, outputRoot, cfg.Targets) {
			continue
		}
		filtered = append(filtered, videoPath)
	}
	return filtered, nil
}

func resolveVideoOutputDir(cfg Config, videoPath string, targetLabel string) string {
	videoDir := filepath.Clean(filepath.Dir(videoPath))
	workDir := filepath.Clean(cfg.WorkDir)
	if samePath(videoDir, workDir) {
		return filepath.Join(cfg.OutputDir, targetLabel)
	}
	return filepath.Join(videoDir, targetLabel)
}

func shouldSkipDiscoveredVideo(videoPath string, workRoot string, outputRoot string, targets []Target) bool {
	if shouldSkipRootOutputVideo(videoPath, workRoot, outputRoot, targets) {
		return true
	}

	parentDir := filepath.Dir(videoPath)
	if !matchesTargetLabel(filepath.Base(parentDir), targets) {
		return false
	}

	// Nested renders are written as <sourceDir>/<targetLabel>/<videoName>.
	// Only skip target-named folders when the original source file exists
	// beside that target folder, so we don't hide legitimate user content.
	sourceCandidate := filepath.Join(filepath.Dir(parentDir), filepath.Base(videoPath))
	info, err := os.Stat(sourceCandidate)
	if err != nil || info.IsDir() {
		return false
	}
	return true
}

func shouldSkipRootOutputVideo(videoPath string, workRoot string, outputRoot string, targets []Target) bool {
	if outputRoot == "" {
		return false
	}

	parentDir := filepath.Dir(videoPath)
	if !matchesTargetLabel(filepath.Base(parentDir), targets) {
		return false
	}

	if samePath(filepath.Dir(parentDir), outputRoot) {
		return true
	}

	// When the configured output directory equals the work directory, we must
	// avoid treating the entire tree as generated output. In that case only the
	// top-level ratio folders are considered root outputs.
	if samePath(outputRoot, workRoot) {
		return false
	}

	return isWithinPath(videoPath, outputRoot)
}

func matchesTargetLabel(name string, targets []Target) bool {
	for _, target := range targets {
		if strings.EqualFold(name, target.Label) {
			return true
		}
	}
	return false
}

func isWithinPath(path string, root string) bool {
	if path == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func samePath(a string, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
