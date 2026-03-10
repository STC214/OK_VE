package ffmpeg

import (
	"strings"
	"testing"
)

func TestPlannedDimensions(t *testing.T) {
	rotate, w, h := PlannedDimensions(1920, 1080)
	if !rotate || w != 1080 || h != 1920 {
		t.Fatalf("unexpected landscape normalization: rotate=%v w=%d h=%d", rotate, w, h)
	}

	rotate, w, h = PlannedDimensions(720, 1280)
	if rotate || w != 720 || h != 1280 {
		t.Fatalf("unexpected portrait normalization: rotate=%v w=%d h=%d", rotate, w, h)
	}
}

func TestBuildFilter(t *testing.T) {
	filter := BuildFilter(true, 1081, 1921, 2401, 20, 30)
	expectedParts := []string{
		"transpose=1",
		"scale=1080:2400:force_original_aspect_ratio=increase",
		"gblur=sigma=20",
		"drawbox=x=0:y=0:w=1080:h=30:t=fill:c=black",
		"overlay=x=0:y=240:shortest=1:format=auto",
	}

	for _, part := range expectedParts {
		if !strings.Contains(filter, part) {
			t.Fatalf("expected filter to contain %q, got %s", part, filter)
		}
	}
}
