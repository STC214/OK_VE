import os
import subprocess
import json
import logging
import re
from tqdm import tqdm

logging.basicConfig(level=logging.INFO,
                    format='%(asctime)s - [%(levelname)s] - %(message)s')


class VideoWallpaperPerfectFeatherEngine:
    def __init__(self, diag_file='ffmpeg_full_diagnostics.json'):
        self.ffmpeg_path = "ffmpeg.exe"
        self.ffprobe_path = "ffprobe.exe"
        if os.path.exists(diag_file):
            with open(diag_file, 'r', encoding='utf-8') as f:
                data = json.load(f)
                self.ffmpeg_path = data['components']['ffmpeg']['path']
                self.ffprobe_path = data['components']['ffprobe']['path']

    def get_video_meta(self, path):
        try:
            cmd = [self.ffprobe_path, '-v', 'error', '-select_streams', 'v:0',
                   '-show_entries', 'stream=width,height,nb_frames', '-of', 'json', path]
            res = subprocess.check_output(cmd).decode('utf-8')
            v_data = json.loads(res)['streams'][0]
            frames = v_data.get('nb_frames')
            return int(v_data['width']), int(v_data['height']), int(frames) if frames and frames != 'N/A' else 0
        except:
            return None

    def build_cmd(self, input_path, out_path, rotate, w, h, target_h):
        # 數值校準
        sw, sh, sth = (w//2)*2, (h//2)*2, (target_h//2)*2
        trans = "transpose=1" if rotate else "copy"

        # 濾鏡邏輯重構：
        # 1. 將前景縮放後的結果命名為 [fg_main]
        # 2. 根據 [fg_main] 的尺寸動態生成遮罩，確保 100% 對齊
        # 3. 強制使用 yuva420p 像素格式以保留 Alpha 羽化通道
        filters = (
            f"[0:v]{trans},setsar=1[raw];"
            f"[raw]split=2[bg_src][fg_src];"
            # 背景層：強制填充畫布並極度模糊
            f"[bg_src]scale={sw}:{sth}:force_original_aspect_ratio=increase,crop={sw}:{sth},gblur=sigma=20[bg];"
            # 前景層：保持比例
            f"[fg_src]scale={sw}:{sh}:force_original_aspect_ratio=decrease,format=yuva420p[fg_main];"
            # 動態遮罩：直接利用 fg_main 的尺寸 (iw, ih)
            f"color=c=black:s={sw}x{sh}[m_temp];"
            f"[m_temp]scale=iw:ih,drawbox=x=20:y=20:w=iw-40:h=ih-40:t=fill:c=white,boxblur=50:2,format=gray[mask_final];"
            # 合併羽化：前景 + 遮罩
            f"[fg_main][mask_final]alphamerge[fg_feathered];"
            # 疊加：在 overlay 中開啟 format=auto 以支援透明度渲染
            f"[bg][fg_feathered]overlay=x=(W-w)/2:y=(H-h)/2:shortest=1:format=auto,format=yuv420p[outv]"
        )

        return [
            self.ffmpeg_path, '-y', '-progress', 'pipe:1', '-nostats', '-loglevel', 'error',
            '-i', input_path,
            '-filter_complex', filters,
            '-map', '[outv]',
            '-c:v', 'h264_nvenc', '-preset', 'p4', '-tune', 'hq', '-b:v', '10M',
            *(['-map', '0:a?', '-c:a', 'copy']),
            out_path
        ]

    def run(self):
        files = [f for f in os.listdir(
            '.') if f.lower().endswith(('.mp4', '.mov'))]
        for f in files:
            meta = self.get_video_meta(f)
            if not meta:
                continue
            ow, oh, total_f = meta
            rotate = ow > oh
            w, h = (oh, ow) if rotate else (ow, oh)

            for label, ratio in [('9x20', 9/20), ('5x11', 5/11)]:
                th = int(w / ratio)
                os.makedirs(f"output/{label}", exist_ok=True)
                out = f"output/{label}/{f}"
                cmd = self.build_cmd(os.path.abspath(f), out, rotate, w, h, th)

                print(f"\n🚀 正在渲染 (羽化增強版): {f} -> {label}")
                with tqdm(total=total_f, unit='f') as pbar:
                    proc = subprocess.Popen(
                        cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
                    last_f = 0
                    for line in proc.stdout:
                        m = re.search(r'frame=(\d+)', line)
                        if m:
                            curr_f = int(m.group(1))
                            pbar.update(curr_f - last_f)
                            last_f = curr_f
                    proc.wait()


if __name__ == "__main__":
    VideoWallpaperPerfectFeatherEngine().run()
