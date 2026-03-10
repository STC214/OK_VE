import os
import subprocess
import json
import logging
import re
import sys
from tqdm import tqdm

# 配置日誌
logging.basicConfig(level=logging.INFO,
                    format='%(asctime)s - [%(levelname)s] - %(message)s')
logger = logging.getLogger("VideoEngine_V2")


class VideoWallpaperProductionEngine:
    def __init__(self):
        self.ffmpeg_path = "ffmpeg.exe"
        self.ffprobe_path = "ffprobe.exe"
        self._find_ffmpeg_components()

    def _find_ffmpeg_components(self):
        """需求0: 遍歷目錄尋找 ffmpeg 組件"""
        logger.info("正在尋找 FFmpeg 組件...")
        search_root = os.path.dirname(os.path.abspath(__file__))
        for root, dirs, files in os.walk(search_root):
            if "ffmpeg.exe" in files:
                self.ffmpeg_path = os.path.join(root, "ffmpeg.exe")
            if "ffprobe.exe" in files:
                self.ffprobe_path = os.path.join(root, "ffprobe.exe")
        logger.info(
            f"組件路徑: FFmpeg={self.ffmpeg_path}, FFprobe={self.ffprobe_path}")

    def get_video_meta(self, path):
        """獲取元數據"""
        try:
            cmd = [self.ffprobe_path, '-v', 'error', '-select_streams', 'v:0',
                   '-show_entries', 'stream=width,height,nb_frames', '-of', 'json', path]
            res = subprocess.check_output(cmd).decode('utf-8')
            v_data = json.loads(res)['streams'][0]
            frames = v_data.get('nb_frames')
            return int(v_data['width']), int(v_data['height']), int(frames) if frames and frames != 'N/A' else 0
        except Exception as e:
            logger.error(f"獲取元數據失敗: {path}, 錯誤: {e}")
            return None

    def build_complex_filter(self, rotate, w, h, target_h):
        """需求 1-6: 構建濾鏡鏈"""
        # 數值確保偶數
        sw, sh = (w // 2) * 2, (h // 2) * 2
        sth = (target_h // 2) * 2
        y_offset = (sth - sh) // 2

        # 旋轉邏輯 (需求1)
        trans = "transpose=1" if rotate else "copy"

        # 濾鏡流程 (需求4, 5):
        # [bg] 軌道1: 放大、裁切、高斯模糊
        # [mask] 軌道2預處理: 生成 30px 收縮羽化遮罩
        # [fg_feathered] 合併羽化
        filter_str = (
            # 準備原始流
            f"[0:v]{trans},setsar=1[fg_raw];"
            # 需求4: 軌道1 背景處理
            f"[fg_raw]split=2[fg_for_bg][fg_for_main];"
            f"[fg_for_bg]scale={sw}:{sth}:force_original_aspect_ratio=increase,crop={sw}:{sth},gblur=sigma=20[bg];"
            # 需求5: 軌道2 臨時視頻3 (收縮羽化)
            f"color=c=white:s={sw}x{sh}[m_base];"
            f"[m_base]drawbox=x=0:y=0:w={sw}:h=30:t=fill:c=black,"       # 上邊界
            f"drawbox=x=0:y={sh-30}:w={sw}:h=30:t=fill:c=black,"    # 下邊界
            f"drawbox=x=0:y=0:w=30:h={sh}:t=fill:c=black,"       # 左邊界
            f"drawbox=x={sw-30}:y=0:w=30:h={sh}:t=fill:c=black,"    # 右邊界
            f"boxblur=30:1,format=gray[mask_feathered];"
            # 前景與遮罩融合 (使用 yuva420p 保留透明度)
            f"[fg_for_main]format=yuva420p[fg_alpha];"
            f"[fg_alpha][mask_feathered]alphamerge[fg_feathered];"
            # 需求5: 居中疊加並修正 overlay 語法
            f"[bg][fg_feathered]overlay=x=0:y={y_offset}:shortest=1:format=auto,format=yuv420p[outv]"
        )
        return filter_str

    def process_file(self, input_path):
        meta = self.get_video_meta(input_path)
        if not meta:
            return
        ow, oh, total_f = meta

        # 需求1: 比例大於 1:1 則旋轉 (保證寬 < 高)
        rotate = ow > oh
        w, h = (oh, ow) if rotate else (ow, oh)

        # 需求2: 計算目標畫幅
        for label, ratio in [('9x20', 9/20), ('5x11', 5/11)]:
            target_h = int(w / ratio)
            os.makedirs(f"output/{label}", exist_ok=True)
            out_name = os.path.basename(input_path)
            out_path = os.path.abspath(f"output/{label}/{out_name}")

            filter_complex = self.build_complex_filter(rotate, w, h, target_h)

            # 需求6 & 7: GPU 加速 (NVENC) + VBR 10M 碼率
            cmd = [
                self.ffmpeg_path, '-y', '-progress', 'pipe:1', '-loglevel', 'error',
                '-i', input_path,
                '-filter_complex', filter_complex,
                '-map', '[outv]',
                '-c:v', 'h264_nvenc',  # GPU 加速編碼
                '-rc:v', 'vbr',       # 可變動態碼率 (需求6)
                '-cq:v', '24',        # 質量控制
                '-b:v', '10M',        # 目標碼率
                '-maxrate:v', '15M',
                '-bufsize:v', '20M',
                '-preset', 'p4',      # 兼顧速度與質量
                '-tune', 'hq',
                '-map', '0:a?', '-c:a', 'copy',  # 保持音頻
                out_path
            ]

            logger.info(f">>> 處理中: {out_name} | 目標: {label}")
            with tqdm(total=total_f, unit='f', desc=label) as pbar:
                proc = subprocess.Popen(
                    cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                last_f = 0
                while True:
                    line = proc.stdout.readline()
                    if not line and proc.poll() is not None:
                        break
                    m = re.search(r'frame=(\d+)', line)
                    if m:
                        curr_f = int(m.group(1))
                        pbar.update(curr_f - last_f)
                        last_f = curr_f

                _, stderr = proc.communicate()
                if proc.returncode != 0:
                    logger.error(
                        f"FFmpeg 錯誤 (返回值 {proc.returncode}):\n{stderr}")

    def run(self):
        # 遍歷當前目錄下的所有影片
        files = [f for f in os.listdir('.') if f.lower().endswith(
            ('.mp4', '.mov', '.mkv', '.avi'))]
        if not files:
            logger.warning("目錄中未找到影片文件。")
            return

        for f in files:
            self.process_file(os.path.abspath(f))
        logger.info("✅ 所有任務已完成。")


if __name__ == "__main__":
    # 執行檢查並運行
    engine = VideoWallpaperProductionEngine()
    engine.run()
