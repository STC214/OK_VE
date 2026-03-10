package app

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"onekeyvego/internal/ffmpeg"
	"onekeyvego/internal/procutil"
)

type Target struct {
	Label string
	Ratio float64
}

type Config struct {
	WorkDir     string
	OutputDir   string
	FFmpegPath  string
	FFprobePath string
	Encoder     string
	BlurSigma   int
	FeatherPx   int
	Targets     []Target
	OnLog       func(string)
	OnProgress  func(ProgressUpdate)
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

func DefaultConfig(workdir string) Config {
	return Config{
		WorkDir:   workdir,
		OutputDir: filepath.Join(workdir, "output"),
		BlurSigma: 20,
		FeatherPx: 30,
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
	if cfg.BlurSigma <= 0 {
		return fmt.Errorf("blur must be positive")
	}
	if cfg.FeatherPx <= 0 {
		return fmt.Errorf("feather must be positive")
	}
	if len(cfg.Targets) == 0 {
		return fmt.Errorf("at least one target ratio is required")
	}
	outputRoot := cfg.OutputDir
	if !filepath.IsAbs(outputRoot) {
		outputRoot = filepath.Join(cfg.WorkDir, outputRoot)
	}
	if isCDrivePath(outputRoot) {
		return fmt.Errorf("refusing to write output under C drive: %s", outputRoot)
	}

	bins, err := ffmpeg.Locate(cfg.WorkDir, ffmpeg.Binaries{
		FFmpeg:  cfg.FFmpegPath,
		FFprobe: cfg.FFprobePath,
	})
	if err != nil {
		return err
	}

	encoder := ffmpeg.DetectPreferredEncoder(bins.FFmpeg, cfg.Encoder)
	emitLog(cfg, "ffmpeg: %s", bins.FFmpeg)
	emitLog(cfg, "ffprobe: %s", bins.FFprobe)
	emitLog(cfg, "binary source: %s", bins.Source)
	emitLog(cfg, "video encoder: %s", encoder)

	videos, err := ffmpeg.FindVideos(cfg.WorkDir)
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
	meta, err := ffmpeg.Probe(bins.FFprobe, videoPath)
	if err != nil {
		return err
	}

	rotate, width, height := ffmpeg.PlannedDimensions(meta.Width, meta.Height)
	videoName := filepath.Base(videoPath)
	emitLog(cfg, "processing %s (source=%dx%d rotate=%v normalized=%dx%d)", videoName, meta.Width, meta.Height, rotate, width, height)

	for index, target := range cfg.Targets {
		taskIndex := completedBase + index
		targetHeight := int(float64(width) / target.Ratio)
		filter := ffmpeg.BuildFilter(rotate, width, height, targetHeight, cfg.BlurSigma, cfg.FeatherPx)
		emitProgress(cfg, ProgressUpdate{
			TotalTasks:     totalTasks,
			CompletedTasks: taskIndex,
			CurrentTask:    taskIndex + 1,
			VideoName:      videoName,
			TargetLabel:    target.Label,
			TotalFrames:    meta.Frames,
			Percent:        percentForTask(taskIndex, totalTasks, 0, meta.Frames),
		})

		outDir := filepath.Join(cfg.OutputDir, target.Label)
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return fmt.Errorf("create output directory %s: %w", outDir, err)
		}

		outPath := filepath.Join(outDir, videoName)
		cmd := buildCommand(bins.FFmpeg, encoder, videoPath, outPath, filter)
		emitLog(cfg, "rendering %s -> %s", videoName, outPath)

		lastFrame := 0
		err := ffmpeg.RunWithProgress(cmd, meta.Frames, func(frame int) {
			emitProgress(cfg, ProgressUpdate{
				TotalTasks:     totalTasks,
				CompletedTasks: taskIndex,
				CurrentTask:    taskIndex + 1,
				VideoName:      videoName,
				TargetLabel:    target.Label,
				CurrentFrame:   frame,
				TotalFrames:    meta.Frames,
				Percent:        percentForTask(taskIndex, totalTasks, frame, meta.Frames),
			})

			if meta.Frames > 0 {
				if frame-lastFrame >= 120 || frame == meta.Frames {
					progress := float64(frame) / float64(meta.Frames) * 100
					emitLog(cfg, "[%s] %s frame=%d/%d %.1f%%", target.Label, videoName, frame, meta.Frames, progress)
					lastFrame = frame
				}
				return
			}

			if frame-lastFrame >= 120 {
				emitLog(cfg, "[%s] %s frame=%d", target.Label, videoName, frame)
				lastFrame = frame
			}
		})
		if err != nil {
			return fmt.Errorf("render %s for %s: %w", target.Label, filepath.Base(videoPath), err)
		}
		emitProgress(cfg, ProgressUpdate{
			TotalTasks:     totalTasks,
			CompletedTasks: taskIndex + 1,
			CurrentTask:    taskIndex + 1,
			VideoName:      videoName,
			TargetLabel:    target.Label,
			CurrentFrame:   meta.Frames,
			TotalFrames:    meta.Frames,
			Percent:        percentForTask(taskIndex+1, totalTasks, meta.Frames, meta.Frames),
		})
	}

	return nil
}

func percentForTask(completedTasks int, totalTasks int, currentFrame int, totalFrames int) float64 {
	if totalTasks <= 0 {
		return 0
	}
	progressUnits := float64(completedTasks)
	if totalFrames > 0 && currentFrame > 0 {
		framePortion := float64(currentFrame) / float64(totalFrames)
		if framePortion > 1 {
			framePortion = 1
		}
		progressUnits += framePortion
	}
	return progressUnits / float64(totalTasks) * 100
}

func buildCommand(ffmpegPath string, encoder string, inputPath string, outputPath string, filter string) *exec.Cmd {
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
			"-cq:v", "24",
			"-b:v", "10M",
			"-maxrate:v", "15M",
			"-bufsize:v", "20M",
			"-preset", "p4",
			"-tune", "hq",
		)
	default:
		args = append(args,
			"-c:v", "libx264",
			"-preset", "medium",
			"-crf", "20",
			"-maxrate:v", "15M",
			"-bufsize:v", "20M",
			"-pix_fmt", "yuv420p",
		)
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

func isCDrivePath(path string) bool {
	if path == "" {
		return false
	}
	volume := strings.ToUpper(filepath.VolumeName(filepath.Clean(path)))
	return volume == "C:"
}
