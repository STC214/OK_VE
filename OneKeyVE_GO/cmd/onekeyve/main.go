package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

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
	flag.StringVar(&cfg.FFmpegPath, "ffmpeg", "", "path to ffmpeg binary")
	flag.StringVar(&cfg.FFprobePath, "ffprobe", "", "path to ffprobe binary")
	flag.StringVar(&cfg.Encoder, "encoder", "", "video encoder override, for example h264_nvenc or libx264")
	flag.IntVar(&cfg.BlurSigma, "blur", cfg.BlurSigma, "background blur sigma")
	flag.IntVar(&cfg.FeatherPx, "feather", cfg.FeatherPx, "foreground feather width in pixels")
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
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
