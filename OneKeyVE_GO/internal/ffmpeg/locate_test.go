package ffmpeg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocatePrefersExecutableDirectoryFFmpegFolder(t *testing.T) {
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "work")
	exeDir := filepath.Join(tempDir, "app")
	ffmpegDir := filepath.Join(exeDir, "ffmpeg", "bin")

	mustMkdir(t, workDir)
	mustWriteFile(t, filepath.Join(ffmpegDir, binaryName("ffmpeg")))
	mustWriteFile(t, filepath.Join(ffmpegDir, binaryName("ffprobe")))

	restoreExecutable := executablePathFunc
	restoreDriveRoots := driveRootsFunc
	t.Cleanup(func() {
		executablePathFunc = restoreExecutable
		driveRootsFunc = restoreDriveRoots
	})

	executablePathFunc = func() (string, error) {
		return filepath.Join(exeDir, "OneKeyVE.exe"), nil
	}
	driveRootsFunc = func() ([]string, error) {
		return nil, nil
	}

	bins, err := Locate(workDir, Binaries{})
	if err != nil {
		t.Fatalf("Locate returned error: %v", err)
	}
	if bins.Source != filepath.Join(exeDir, "ffmpeg", "bin") {
		t.Fatalf("expected executable directory source, got %q", bins.Source)
	}
}

func TestLocateIncludesDriveRootFFmpegFolder(t *testing.T) {
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "work")
	driveRoot := filepath.Join(tempDir, "driveC")
	ffmpegDir := filepath.Join(driveRoot, "ffmpeg", "bin")

	mustMkdir(t, workDir)
	mustWriteFile(t, filepath.Join(ffmpegDir, binaryName("ffmpeg")))
	mustWriteFile(t, filepath.Join(ffmpegDir, binaryName("ffprobe")))

	restoreExecutable := executablePathFunc
	restoreDriveRoots := driveRootsFunc
	t.Cleanup(func() {
		executablePathFunc = restoreExecutable
		driveRootsFunc = restoreDriveRoots
	})

	executablePathFunc = func() (string, error) {
		return filepath.Join(tempDir, "missing", "OneKeyVE.exe"), nil
	}
	driveRootsFunc = func() ([]string, error) {
		return []string{driveRoot}, nil
	}

	bins, err := Locate(workDir, Binaries{})
	if err != nil {
		t.Fatalf("Locate returned error: %v", err)
	}
	if bins.Source != filepath.Join(driveRoot, "ffmpeg", "bin") {
		t.Fatalf("expected drive-root ffmpeg source, got %q", bins.Source)
	}
}

func TestLocateIncludesSystemPath(t *testing.T) {
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "work")
	pathDir := filepath.Join(tempDir, "pathbin")

	mustMkdir(t, workDir)
	mustWriteFile(t, filepath.Join(pathDir, binaryName("ffmpeg")))
	mustWriteFile(t, filepath.Join(pathDir, binaryName("ffprobe")))

	restoreExecutable := executablePathFunc
	restoreDriveRoots := driveRootsFunc
	t.Cleanup(func() {
		executablePathFunc = restoreExecutable
		driveRootsFunc = restoreDriveRoots
	})

	executablePathFunc = func() (string, error) {
		return filepath.Join(tempDir, "missing", "OneKeyVE.exe"), nil
	}
	driveRootsFunc = func() ([]string, error) {
		return nil, nil
	}

	t.Setenv("PATH", pathDir)

	bins, err := Locate(workDir, Binaries{})
	if err != nil {
		t.Fatalf("Locate returned error: %v", err)
	}
	if bins.Source != "PATH" {
		t.Fatalf("expected PATH source, got %q", bins.Source)
	}
}

func TestChooseRuntimeDirRejectsCAndUsesEnv(t *testing.T) {
	t.Setenv("ONEKEYVE_RUNTIME_DIR", `C:\temp\blocked`)
	t.Setenv("ONEKEYVE_CACHE_DIR", `F:\runtime-ok`)
	t.Setenv("TMPDIR", "")
	t.Setenv("TEMP", "")
	t.Setenv("TMP", "")

	dir, err := chooseRuntimeDir(`F:\work`)
	if err != nil {
		t.Fatalf("chooseRuntimeDir returned error: %v", err)
	}
	expected := filepath.Join(`F:\runtime-ok`, "OneKeyVE")
	if dir != expected {
		t.Fatalf("expected %q, got %q", expected, dir)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
