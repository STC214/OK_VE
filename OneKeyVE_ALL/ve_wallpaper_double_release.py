import os
import subprocess
import json
import logging
import re
import sys
import time
from pathlib import Path
from tqdm import tqdm

# ==========================================
# 1. 视觉增强库引入 (Rich Library)
# ==========================================
try:
    from rich.console import Console
    from rich.panel import Panel
    from rich.progress import Progress, SpinnerColumn, TextColumn, BarColumn, TaskProgressColumn, TimeRemainingColumn
    from rich.logging import RichHandler
    from rich import print as rprint
    HAS_RICH = True
    console = Console()
except ImportError:
    HAS_RICH = False

# 日志配置：如果支持 Rich 则显示彩色日志
logging.basicConfig(
    level=logging.INFO,
    format='%(message)s',
    handlers=[RichHandler(rich_tracebacks=True)
              if HAS_RICH else logging.StreamHandler()]
)
logger = logging.getLogger("VideoEngine")


class UltimateVideoEngine:
    def __init__(self):
        # 默认组件名称，将在初始化中动态更新
        self.ffmpeg_path = "ffmpeg.exe"
        self.ffprobe_path = "ffprobe.exe"
        self._find_components()

    # ==========================================
    # 2. 组件路径搜索 (需求 0)
    # ==========================================
    def _find_components(self):
        """递归搜索当前目录及子目录，寻找 FFmpeg 诊断文件中指定的路径"""
        search_root = Path(".").resolve()
        diag_file = search_root / "ffmpeg_full_diagnostics.json"

        if diag_file.exists():
            with open(diag_file, 'r', encoding='utf-8') as f:
                data = json.load(f)
                # 严格按照诊断文件规范提取路径
                self.ffmpeg_path = data['components']['ffmpeg']['path']
                self.ffprobe_path = data['components']['ffprobe']['path']
        else:
            # 备选方案：如果 JSON 不存在，在当前目录下递归查找可执行文件
            for p in search_root.rglob("ffmpeg.exe"):
                self.ffmpeg_path = str(p)
                break

        if HAS_RICH:
            rprint(Panel(
                f"[bold green]组件加载成功[/bold green]\n[dim]FFmpeg: {self.ffmpeg_path}[/dim]", title="系统状态"))

    # ==========================================
    # 3. 视频元数据解析
    # ==========================================
    def get_video_meta(self, path):
        """利用 ffprobe 获取视频的宽高和总帧数"""
        try:
            cmd = [self.ffprobe_path, '-v', 'error', '-select_streams', 'v:0',
                   '-show_entries', 'stream=width,height,nb_frames', '-of', 'json', path]
            res = subprocess.check_output(cmd).decode('utf-8')
            v_data = json.loads(res)['streams'][0]
            frames = v_data.get('nb_frames')
            # 宽高必须是偶数才能被大多数编码器识别
            return int(v_data['width']), int(v_data['height']), int(frames) if frames and frames != 'N/A' else 0
        except:
            return None

    # ==========================================
    # 4. FFmpeg 核心滤镜链构建 (关键逻辑)
    # ==========================================
    def build_filter(self, rotate, w, h, target_h):
        """
        构建复杂的 FilterGraph 以实现背景模糊与内缩羽化
        rotate: 是否需要 90 度顺时针旋转
        w, h: 旋转后的视频宽高
        target_h: 目标画幅总高度
        """
        # 强制偶数化处理，防止 FFmpeg 报错
        sw, sh = (w // 2) * 2, (h // 2) * 2
        sth = (target_h // 2) * 2
        y_offset = (sth - sh) // 2  # 计算前景居中的垂直偏移

        # 需求 1: 旋转处理
        trans = "transpose=1" if rotate else "copy"

        # 滤镜链详解：
        # [raw]: 旋转处理后的原始流
        # [bg]: 轨道 1 - 放大 -> 裁剪 -> 高斯模糊 (20)
        # [mask]: 轨道 2 预处理 - 创建纯色画布 -> 绘制 30px 黑边 -> 盒状模糊(羽化)
        # [fg_final]: 前景合并 - 使用 alphamerge 将遮罩应用到视频上
        # [outv]: 最终叠加 - overlay 必须开启 format=auto 才能支持 Alpha 通道渲染
        filters = (
            f"[0:v]{trans},setsar=1[raw];"
            f"[raw]split=2[bg_src][fg_src];"
            f"[bg_src]scale={sw}:{sth}:force_original_aspect_ratio=increase,crop={sw}:{sth},gblur=sigma=20[bg];"
            f"color=c=white:s={sw}x{sh}[m_base];"
            f"[m_base]drawbox=x=0:y=0:w={sw}:h=30:t=fill:c=black,"  # 上边羽化区
            f"drawbox=x=0:y={sh-30}:w={sw}:h=30:t=fill:c=black,"  # 下边羽化区
            f"drawbox=x=0:y=0:w=30:h={sh}:t=fill:c=black,"       # 左边羽化区
            f"drawbox=x={sw-30}:y=0:w=30:h={sh}:t=fill:c=black,"  # 右边羽化区
            f"boxblur=30:1,format=gray[mask];"
            f"[fg_src]format=yuva420p[fg_alpha];"
            f"[fg_alpha][mask]alphamerge[fg_final];"
            f"[bg][fg_final]overlay=x=0:y={y_offset}:shortest=1:format=auto,format=yuv420p[outv]"
        )
        return filters

    # ==========================================
    # 5. 任务分发与 GPU 编码执行
    # ==========================================
    def process_task(self, video_path):
        meta = self.get_video_meta(str(video_path))
        if not meta:
            return
        ow, oh, total_f = meta

        # 需求 1: 比例判断 (大于 1:1 则旋转)
        rotate = ow > oh
        w, h = (oh, ow) if rotate else (ow, oh)

        # 需求 2: 处理两个目标比例
        for label, ratio in [('9x20', 9/20), ('5x11', 5/11)]:
            th = int(w / ratio)
            out_dir = Path(f"output/{label}")
            out_dir.mkdir(parents=True, exist_ok=True)
            out_path = out_dir / video_path.name

            filter_str = self.build_filter(rotate, w, h, th)

            # 需求 6 & 7: GPU 加速配置
            # 使用 h264_nvenc (NVIDIA 显卡加速)
            # rc:v vbr -> 启用可变动态码率
            # b:v 10M -> 目标平均码率
            cmd = [
                self.ffmpeg_path, '-y', '-progress', 'pipe:1', '-loglevel', 'error',
                '-i', str(video_path),
                '-filter_complex', filter_str,
                '-map', '[outv]',
                '-c:v', 'h264_nvenc', '-rc:v', 'vbr', '-b:v', '10M', '-maxrate:v', '15M',
                '-preset', 'p4', '-tune', 'hq',
                '-map', '0:a?', '-c:a', 'copy',  # 复制原音轨，不重新编码
                str(out_path)
            ]

            self.run_with_progress(
                cmd, total_f, f"[{label}] {video_path.name}")

    # ==========================================
    # 6. 装饰性进度条逻辑
    # ==========================================
    def run_with_progress(self, cmd, total_frames, description):
        """实时捕获 FFmpeg stdout 管道中的 frame 字段更新进度条"""
        if HAS_RICH:
            with Progress(
                SpinnerColumn(),
                TextColumn("[progress.description]{task.description}"),
                BarColumn(bar_width=None),
                TaskProgressColumn(),
                TimeRemainingColumn(),
                console=console
            ) as progress:
                task = progress.add_task(description, total=total_frames)
                proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, text=True)
                last_f = 0
                while True:
                    line = proc.stdout.readline()
                    if not line and proc.poll() is not None:
                        break
                    m = re.search(r'frame=(\d+)', line)
                    if m:
                        curr_f = int(m.group(1))
                        progress.update(task, advance=curr_f - last_f)
                        last_f = curr_f
        else:
            # 备选：标准 tqdm 进度条
            with tqdm(total=total_frames, desc=description, unit='f') as pbar:
                proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, text=True)
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

    def start(self):
        """自动扫描当前目录并启动引擎"""
        video_exts = ('.mp4', '.mov', '.mkv', '.avi')
        video_files = [Path(f) for f in os.listdir(
            '.') if f.lower().endswith(video_exts)]

        if not video_files:
            rprint("[bold red]❌ 未在当前目录发现视频文件。[/bold red]")
            return

        rprint(
            f"[bold cyan]🚀 发现 {len(video_files)} 个任务，正在启动 GPU 加速引擎...[/bold cyan]\n")

        for v in video_files:
            start_time = time.time()
            self.process_task(v)
            elapsed = time.time() - start_time
            rprint(
                f"[bold green]✅ 任务完成:[/bold green] {v.name} [dim](耗时: {elapsed:.1f}s)[/dim]")


if __name__ == "__main__":
    try:
        engine = UltimateVideoEngine()
        engine.start()
        rprint("\n[bold reverse green] ✨ 全部视频批量处理完毕！ ✨ [/bold reverse green]")
    except KeyboardInterrupt:
        rprint("\n[bold red]中止操作：用户手动中断了程序。[/bold red]")
