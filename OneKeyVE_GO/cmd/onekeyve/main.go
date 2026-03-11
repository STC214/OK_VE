package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"onekeyvego/internal/app"
)

func main() {
	workdir, err := os.Getwd()
	if err != nil {
		log.Fatalf("resolve working directory: %v", err)
	}

	cfg := app.DefaultConfig(workdir)
	applyEnvDefaults(&cfg, workdir)

	flag.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "directory that contains input videos")
	flag.StringVar(&cfg.OutputDir, "output", cfg.OutputDir, "directory that will contain rendered videos")
	flag.StringVar(&cfg.FFmpegPath, "ffmpeg", cfg.FFmpegPath, "path to ffmpeg binary")
	flag.StringVar(&cfg.FFprobePath, "ffprobe", cfg.FFprobePath, "path to ffprobe binary")
	flag.StringVar(&cfg.Encoder, "encoder", cfg.Encoder, "video encoder override, for example h264_nvenc or libx264")
	flag.StringVar(&cfg.BlackBorderMode, "black-mode", cfg.BlackBorderMode, "black border mode: center_crop or legacy")
	flag.IntVar(&cfg.BlurSigma, "blur", cfg.BlurSigma, "background blur sigma")
	flag.IntVar(&cfg.FeatherPx, "feather", cfg.FeatherPx, "foreground feather width in pixels")
	flag.IntVar(&cfg.BlackLineThreshold, "black-threshold", cfg.BlackLineThreshold, "luma threshold for pure-black border lines")
	flag.IntVar(&cfg.BlackLineRatioPercent, "black-ratio", cfg.BlackLineRatioPercent, "minimum black-pixel ratio percent for a border line")
	flag.IntVar(&cfg.BlackLineRequiredRun, "black-run", cfg.BlackLineRequiredRun, "minimum consecutive border lines required")
	flag.Parse()

	if err := app.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func applyEnvDefaults(cfg *app.Config, fallbackWorkdir string) {
	cfg.WorkDir = firstNonEmpty(os.Getenv("ONEKEYVE_WORKDIR"), cfg.WorkDir)
	if cfg.WorkDir == "" {
		cfg.WorkDir = fallbackWorkdir
	}

	cfg.OutputDir = firstNonEmpty(
		os.Getenv("ONEKEYVE_OUTPUT"),
		os.Getenv("ONEKEYVE_OUTPUT_DIR"),
		cfg.OutputDir,
		filepath.Join(cfg.WorkDir, "output"),
	)
	cfg.FFmpegPath = firstNonEmpty(os.Getenv("ONEKEYVE_FFMPEG"), os.Getenv("FFMPEG_PATH"))
	cfg.FFprobePath = firstNonEmpty(os.Getenv("ONEKEYVE_FFPROBE"), os.Getenv("FFPROBE_PATH"))
	cfg.Encoder = firstNonEmpty(os.Getenv("ONEKEYVE_ENCODER"), cfg.Encoder)
	cfg.BlackBorderMode = firstNonEmpty(os.Getenv("ONEKEYVE_BLACK_MODE"), cfg.BlackBorderMode)
	cfg.BlackLineThreshold = firstPositiveInt(os.Getenv("ONEKEYVE_BLACK_THRESHOLD"), cfg.BlackLineThreshold)
	cfg.BlackLineRatioPercent = firstPositiveInt(os.Getenv("ONEKEYVE_BLACK_RATIO"), cfg.BlackLineRatioPercent)
	cfg.BlackLineRequiredRun = firstPositiveInt(os.Getenv("ONEKEYVE_BLACK_RUN"), cfg.BlackLineRequiredRun)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstPositiveInt(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
