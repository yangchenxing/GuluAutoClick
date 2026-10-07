# auto_click 设计文档（Go 复刻版）

> 复刻来源：`adb_auto_click.py` + `adb_screenshot.py`（Python）。
> 目标平台：Windows；编译产物为单个 `auto_click.exe`。
> 目标行为与 Python 版**完全等价**：周期截取设备画面指定区域，平均颜色不在设定范围时点击该位置。

---

## 1. 程序结构

```
auto_click/
├── main.go        // GUI（窗口、按钮、日志控件）、状态机、日志通道
└── worker.go      // 轮询循环：截屏 → 裁剪 → 平均色 → 判断 → 点击
```

- `adb.exe` 与 `auto_click.exe` **同目录**，程序启动时用 `os.Executable()` 定位它（替代 Python 版的 PATH/ANDROID_HOME 查找）。
- 不读任何配置文件，所有参数为编译期常量（见 §2）。
- 单 goroutine 工作循环 + GUI 主线程，两者之间用 channel 传递日志行；点按钮只切换运行/暂停标志，不杀 goroutine。

## 2. 参数（原配置文件 `adb_auto_click.yaml` 的具体值，硬编码）

| 参数 | 值 | 含义 |
|---|---|---|
| `Host` | `127.0.0.1` | adb 主机 |
| `Port` | `5555` | adb 端口 |
| `Serial` | `127.0.0.1:5555` | 设备序列号（Host:Port） |
| `X` | `844` | 检测区域中心 x / 点击 x（设备屏幕像素坐标，左上角为原点，向右为 x、向下为 y） |
| `Y` | `1632` | 检测区域中心 y / 点击 y |
| `R` | `32` | 正方形区域半径 |
| `Interval` | `2` 秒 | 检查间隔 |
| `RangeR` | `[150, 155]` | R 通道闭区间 [最小值, 最大值]，含两端 |
| `RangeG` | `[133, 140]` | G 通道闭区间 |
| `RangeB` | `[92, 98]` | B 通道闭区间 |

**判定规则**：三个通道**全部**落在各自闭区间内才算"在范围内"；任一通道越界即为"超出范围"→ 点击一次。

**超时与常量**：

| 常量 | 值 | 用途 |
|---|---|---|
| `ADB_TIMEOUT` | 30 s | `adb devices` / `adb connect` |
| `CAPTURE_TIMEOUT` | 60 s | `adb exec-out screencap -p` |
| `TAP_TIMEOUT` | 20 s | `adb shell input tap` |
| PNG 签名 | `89 50 4E 47 0D 0A 1A 0A` | 校验截屏返回的是 PNG |

区域大小：`X-R ~ X+R`、`Y-R ~ Y+R`，含两端 → 共 `(2R+1) × (2R+1) = 65 × 65` 像素。

## 3. 启动流程

1. 定位 `adb.exe`：取 `filepath.Dir(os.Executable()) + "\adb.exe"`；不存在则弹错误日志。
2. **设备就绪检查** `ensureDevice()`：
   1. `adb devices`（超时 30 s），解析输出：跳过首行表头，每行按空白切分，前两列构成 `序列号 → 状态` 映射。
   2. 若 `devices[Serial] == "device"`，直接通过。
   3. 否则执行 `adb connect 127.0.0.1:5555`，把命令输出打到日志。
   4. 再跑一次 `adb devices` 复查；仍不是 `device`：
      - 列表里没有 → 错误"连接失败：请确认模拟器已启动且 5555 端口可连"
      - 状态是 `unauthorized` 等 → 错误"设备状态为 X，需在设备上允许调试授权"。
3. 打出一行启动信息（设备、中心/半径、RGB 范围、间隔），然后进入轮询。

所有 adb 调用统一走一个 helper：`runADB(ctx, timeout, args...) (stdout []byte, exitCode int, err error)`。
超时返回错误"adb xxx 超时（30s）"；进程无法启动返回"无法执行 adb"。

## 4. 轮询循环（worker）

```go
for {
    if paused { wait(); continue }        // 暂停：阻塞在 channel 上，不截屏
    started := time.Now()

    // 1. 截屏
    data := adb("-s", Serial, "exec-out", "screencap", "-p")  // 60s 超时
    if !bytes.HasPrefix(data, pngSig) { 错误("截屏失败：未返回 PNG"); break }

    // 2. 计算裁剪框（越界则收进画面内）
    w, h := pngSize(data)                // 见 §5
    left, top   := max(X-R, 0),     max(Y-R, 0)
    right, bottom := min(X+R, w-1), min(Y+R, h-1)
    if left > right || top > bottom { 错误("裁剪区域超出画面"); break }
    if (left,top,right,bottom) != 请求框 && 未告警过 { 日志("请求区域超出画面，已收进"); 置已告警 }

    // 3. 裁剪 + 平均色
    rgb := avgRGB(data, left, top, right, bottom)   // (R,G,B)，逐通道算术平均，向下取整

    // 4. 判定
    checks++
    stamp := time.Now().Format("15:04:05")
    if inRange(rgb) {
        日志("[%s] RGB=(%3d, %3d, %3d) 在范围内", ...)
    } else {
        outside++
        adb("-s", Serial, "shell", "input", "tap", X, Y)   // 20s 超时
        if 成功 { taps++; 日志("... 超出范围 -> 已点击 (844, 1632)") }
        else     { 日志("... 超出范围 -> 点击失败（返回码 n）：stderr") }
    }

    // 5. 节拍对齐：sleep 剩余间隔，使每轮耗时恒为 Interval
    time.Sleep(max(0, Interval - time.Since(started)))
}
```

- 任何一步的致命错误（adb 找不到/超时/截屏失败/裁剪越界）都终止循环、打错误日志并回到"停止"状态。
- 循环退出时打统计：`结束：共检查 n 次，其中 m 次超出范围，实际点击 k 次`。

## 5. PNG 处理（不依赖第三方库）

Go 标准库 `image/png` 足够，两处小技巧：

**a) 读 PNG 尺寸（不整图解码）** —— 等价于 Python 的 `png_size()`：

```go
func pngSize(data []byte) (int, int) {
    if len(data) >= 24 && string(data[12:16]) == "IHDR" {
        w := binary.BigEndian.Uint32(data[16:20])
        h := binary.BigEndian.Uint32(data[20:24])
        return int(w), int(h)
    }
    return 0, 0
}
```

**b) 平均 RGB** —— 用 `png.Decode` 整图解码后裁剪（或解码后 `img.At` 遍历目标矩形），逐通道累加后 `sum / pixelCount`（整数除法即向下取整）：

```go
img, _ := png.Decode(bytes.NewReader(data))
sumR, sumG, sumB, n := 0, 0, 0, 0
for y := top; y <= bottom; y++ {          // 含两端
    for x := left; x <= right; x++ {      // 含两端
        r16, g16, b16, _ := img.At(x, y).RGBA()
        sumR += int(r16 >> 8)             // 16bit → 8bit
        sumG += int(g16 >> 8)
        sumB += int(b16 >> 8)
        n++
    }
}
avgR, avgG, avgB := sumR/n, sumG/n, sumB/n   // 向下取整
```

注意 Go 的坐标与 Python PIL `crop` 一致为半开区间 `[left, right+1) × [top, bottom+1)`，所以循环边界要用 `<= right`、`<= bottom`。

`inRange` 判定：

```go
func inRange(r, g, b int) bool {
    return r >= 150 && r <= 155 &&
           g >= 133 && g <= 140 &&
           b >= 92  && b <= 98
}
```

## 6. GUI 设计（Fyne，跨 Win/macOS/Linux）

采用 `fyne.io/fyne/v2`（纯 Go 无第三方运行时依赖，控件线程安全，可交叉编译单 exe）。

**界面**：一个主窗口，仅两个控件——

1. **Button**：文本在 `开始` / `暂停` / `继续` 之间切换。
   - 初始 `开始`。点击 `开始` → 启动 worker goroutine（内部先跑 `ensureDevice`），按钮变 `暂停`。
   - 点击 `暂停` → 循环阻塞在暂停通道上；按钮变 `继续`。再点恢复。
   - 不需要停止按钮——窗口关闭即退出进程。
2. **RichText（包在 VScroll 里）**：滚动输出所有日志行（截屏结果、点击、错误、统计）。
   - 注意 Fyne v2.8 已移除 `Entry.ReadOnly`，日志展示用 `RichText` + `TextSegment` 而非 Entry。

**线程模型**（Fyne 控件 goroutine-safe，比 walk 简单）：

- worker 只把日志字符串 push 进 `logCh chan string`（容量 256，阻塞式）。
- 专门的 UI goroutine 消费 `logCh`，刷新 `TextSegment.Text` + `Refresh()`，`container.ScrollToBottom()` 滚到底；日志超 256KB 截尾。
- 运行/暂停用 `pauseCtl`（mutex + `chan struct{}`）：`paused` 时 worker 阻塞在通道上直到 `resume` 关闭它。
- 暂停只影响轮询循环入口，正在执行的 adb 命令会跑完当前这一轮。

**构建**（`project/build.sh` 已封装）：

```sh
# macOS
go build -o dist/mac/auto_click .
# Windows 交叉编译需要 mingw-w64（brew install mingw-w64），Fyne 桌面端必须 CGO
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc go build -o dist/windows/auto_click.exe .
```

**日志格式**与 Python 版保持一致，便于对照排查：

```
配置：设备 127.0.0.1:5555，中心 (844, 1632)，半径 32 -> 区域 65x65 像素，间隔 2s
开始轮询
[23:16:01] RGB=(152, 136, 95) 在范围内
[23:16:03] RGB=( 90,  88, 210) 超出范围 -> 已点击 (844, 1632)
结束：共检查 128 次，其中 4 次超出范围，实际点击 4 次
```

## 7. 与原版的差异（有意为之）

| 项 | Python 版 | Go 版 |
|---|---|---|
| 配置 | `adb_auto_click.yaml`（不存在则生成默认） | 硬编码常量 §2 |
| adb 定位 | PATH + ANDROID_HOME + 常见路径 | 仅 exe 同目录 `adb.exe` |
| 平台 | 跨平台 | 仅 Windows |
| dry-run | 有 `--dry-run` | 不需要 |
| CLI 参数 | `--config/--port/--adb` | 无 |
| 计时 | 保持每轮固定间隔（对齐节拍） | 保留 |

其余行为（设备就绪流程、裁剪含端点、向下取整平均色、三通道闭区间判定、点击超时 20 s、越界裁剪告警只打一次）均与原版一一对应。
