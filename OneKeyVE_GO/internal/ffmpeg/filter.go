package ffmpeg

import "fmt"

type RatioTarget struct {
	Label string
	Ratio float64
}

type CropRect struct {
	X      int
	Y      int
	Width  int
	Height int
}

func (c CropRect) ActiveWidth(fallback int) int {
	if c.Width > 0 {
		return c.Width
	}
	return fallback
}

func (c CropRect) ActiveHeight(fallback int) int {
	if c.Height > 0 {
		return c.Height
	}
	return fallback
}

func (c CropRect) HasCrop() bool {
	return c.Width > 0 && c.Height > 0
}

func (c CropRect) Filter() string {
	return fmt.Sprintf("crop=%d:%d:%d:%d,", Even(c.Width), Even(c.Height), maxInt(c.X, 0), maxInt(c.Y, 0))
}

func Even(n int) int {
	if n < 0 {
		return 0
	}
	return (n / 2) * 2
}

func PlannedDimensions(width int, height int) (rotate bool, normalizedWidth int, normalizedHeight int) {
	if width > height {
		return true, height, width
	}
	return false, width, height
}

func BuildFilter(rotate bool, width int, height int, crop CropRect, targetWidth int, targetHeight int, blurSigma int, featherPx int) string {
	sw := Even(width)
	sh := Even(height)
	tw := Even(targetWidth)
	th := Even(targetHeight)
	if tw <= 0 {
		tw = sw
	}
	if th <= 0 {
		th = sh
	}
	fh := Even(int(float64(sh) * float64(tw) / float64(sw)))
	if fh <= 0 {
		fh = sh
	}
	offsetY := (th - fh) / 2

	transform := "copy"
	if rotate {
		transform = "transpose=1"
	}
	prefix := ""
	if crop.HasCrop() {
		prefix = crop.Filter()
	}

	return fmt.Sprintf(
		"[0:v]%s%s,setsar=1[raw];"+
			"[raw]split=2[bg_src][fg_src];"+
			"[bg_src]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,gblur=sigma=%d[bg];"+
			"[fg_src]scale=%d:%d,setsar=1[fg_scaled];"+
			"color=c=white:s=%dx%d[m_base];"+
			"[m_base]drawbox=x=0:y=0:w=%d:h=%d:t=fill:c=black,"+
			"drawbox=x=0:y=%d:w=%d:h=%d:t=fill:c=black,"+
			"drawbox=x=0:y=0:w=%d:h=%d:t=fill:c=black,"+
			"drawbox=x=%d:y=0:w=%d:h=%d:t=fill:c=black,"+
			"boxblur=%d:1,format=gray[mask];"+
			"[fg_scaled]format=yuva420p[fg_alpha];"+
			"[fg_alpha][mask]alphamerge[fg_final];"+
			"[bg][fg_final]overlay=x=0:y=%d:shortest=1:format=auto,format=yuv420p[outv]",
		prefix,
		transform,
		tw, th, tw, th, blurSigma,
		tw, fh,
		tw, fh,
		tw, featherPx,
		fh-featherPx, tw, featherPx,
		featherPx, fh,
		tw-featherPx, featherPx, fh,
		featherPx,
		offsetY,
	)
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
