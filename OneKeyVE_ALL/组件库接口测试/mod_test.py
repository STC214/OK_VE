import inspect
import pkgutil
import importlib
import json
import os
import sys
import logging

# 配置日誌
logging.basicConfig(level=logging.INFO, format='%(levelname)s: %(message)s')
logger = logging.getLogger(__name__)


def extract_callable_info(func):
    """提取函數簽名，處理不可序列化的預設值"""
    try:
        if not (inspect.isfunction(func) or inspect.ismethod(func)):
            return None

        signature = inspect.signature(func)
        params = {}
        for p_name, p_obj in signature.parameters.items():
            # 轉換預設值為字符串以確保 JSON 安全
            default_val = None
            if p_obj.default is not inspect.Parameter.empty:
                if isinstance(p_obj.default, (str, int, float, bool, type(None))):
                    default_val = p_obj.default
                else:
                    default_val = str(p_obj.default)

            params[p_name] = {
                "default": default_val,
                "kind": str(p_obj.kind),
                "annotation": str(p_obj.annotation) if p_obj.annotation is not inspect.Signature.empty else "any"
            }

        return {
            "signature": str(signature),
            "parameters": params,
            "doc": inspect.getdoc(func)
        }
    except Exception:
        return {"signature": "unavailable", "doc": inspect.getdoc(func)}


def get_detailed_spec(package_name):
    """
    深度掃描庫規範。
    採用手動遞歸遍歷以避開 pkgutil.walk_packages 內部的自動導入崩潰問題。
    """
    try:
        main_package = importlib.import_module(package_name)
    except ImportError:
        logger.error(f"無法加載庫: {package_name}")
        return None

    spec_data = {
        "library_name": package_name,
        "version": getattr(main_package, "__version__", "unknown"),
        "modules": {}
    }

    # 待掃描隊列 (模塊名, 路徑)
    queue = [(package_name, main_package.__path__)]
    visited = set()

    while queue:
        curr_mod_name, curr_path = queue.pop(0)
        if curr_mod_name in visited:
            continue
        visited.add(curr_mod_name)

        try:
            # 嘗試導入模塊
            module = importlib.import_module(curr_mod_name)

            module_info = {"doc": inspect.getdoc(
                module), "functions": {}, "classes": {}}

            # 提取函數
            for name, obj in inspect.getmembers(module, inspect.isfunction):
                if getattr(obj, "__module__", "") == curr_mod_name and not name.startswith("_"):
                    info = extract_callable_info(obj)
                    if info:
                        module_info["functions"][name] = info

            # 提取類
            for name, obj in inspect.getmembers(module, inspect.isclass):
                if getattr(obj, "__module__", "") == curr_mod_name and not name.startswith("_"):
                    class_info = {"doc": inspect.getdoc(obj), "methods": {}}
                    for m_name, m_obj in inspect.getmembers(obj, lambda x: inspect.isfunction(x) or inspect.ismethod(x)):
                        if not m_name.startswith("_") or m_name == "__init__":
                            info = extract_callable_info(m_obj)
                            if info:
                                class_info["methods"][m_name] = info
                    module_info["classes"][name] = class_info

            if module_info["functions"] or module_info["classes"]:
                spec_data["modules"][curr_mod_name] = module_info
                logger.info(f"成功解析: {curr_mod_name}")

            # 尋找子模塊 (手動迭代而不使用 walk_packages 以獲得更高控制權)
            if hasattr(module, "__path__"):
                for _, sub_name, is_pkg in pkgutil.iter_modules(module.__path__):
                    full_sub_name = f"{curr_mod_name}.{sub_name}"
                    if "._" not in full_sub_name and ".tests" not in full_sub_name:
                        queue.append((full_sub_name, module.__path__))

        except Exception as e:
            logger.warning(
                f"跳過模塊 {curr_mod_name}: {type(e).__name__} - {str(e)}")
            continue

    return spec_data


def save_to_json(data, filename):
    try:
        with open(filename, 'w', encoding='utf-8') as f:
            json.dump(data, f, ensure_ascii=False, indent=2)
        print(f"\n[完成] 規範已寫入: {filename}")
    except Exception as e:
        print(f"[錯誤] 保存失敗: {e}")


if __name__ == "__main__":
    # 預先處理可能的遞歸限制
    sys.setrecursionlimit(2000)

    # 處理 VidGear
    print("開始分析 VidGear...")
    vidgear_spec = get_detailed_spec("vidgear")
    if vidgear_spec:
        save_to_json(vidgear_spec, "vidgear_full_spec.json")

    print("\n" + "="*40 + "\n")

    # 處理 ffmpeg-python
    print("開始分析 ffmpeg-python...")
    ffmpeg_spec = get_detailed_spec("ffmpeg")
    if ffmpeg_spec:
        save_to_json(ffmpeg_spec, "ffmpeg_python_full_spec.json")
