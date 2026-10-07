package main

// 轮询核心：周期截取设备画面指定区域的平均颜色，越界则点击。
// 逻辑与 Python 版 adb_auto_click.py + adb_screenshot.py 等价：
//   - adb 取 exe 同目录（Windows 为 adb.exe，其余为 adb）
//   - 设备就绪：adb devices 检查，必要时 adb connect
//   - 每轮：screencap -p 截屏 -> 裁剪 (x-r, y-r)~(x+r, y+r)（含两端，越界收进画面）
//          -> 逐通道算术平均（向下取整）-> 三通道闭区间判定 -> input tap
//   - 节拍对齐：每轮固定 interval

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// 硬编码参数（原 adb_auto_click.yaml）
const (
	host     = "127.0.0.1"
	port     = 5555
	centerX  = 844
	centerY  = 1632
	radius   = 32
	interval = 2 * time.Second

	rangeRLow, rangeRHigh = 150, 155
	rangeGLow, rangeGHigh = 133, 140
	rangeBLow, rangeBHigh = 92, 98
)

const (
	adbTimeout     = 30 * time.Second // devices / connect
	captureTimeout = 60 * time.Second // screencap
	tapTimeout     = 20 * time.Second // input tap
)

var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

var serial = fmt.Sprintf("%s:%d", host, port)

// adbPath 定位与 exe 同目录的 adb（Windows 为 adb.exe）。
func adbPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("无法定位自身路径：%w", err)
	}
	name := "adb"
	if runtime.GOOS == "windows" {
		name = "adb.exe"
	}
	path := filepath.Join(filepath.Dir(exe), name)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return "", fmt.Errorf("未找到 adb：%s（请将 %s 放在 auto_click 同目录）", path, name)
	}
	return path, nil
}

// runADB 执行 adb，超时返回错误。exitCode 为进程退出码（进程启动失败时为 -1）。
func runADB(adb string, timeout time.Duration, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, adb, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return outBuf.Bytes(), errBuf.Bytes(), -1, fmt.Errorf("adb %s 超时（%gs）", strings.Join(args, " "), timeout.Seconds())
	}
	if runErr == nil {
		return outBuf.Bytes(), errBuf.Bytes(), 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return outBuf.Bytes(), errBuf.Bytes(), exitErr.ExitCode(), nil
	}
	return outBuf.Bytes(), errBuf.Bytes(), -1, fmt.Errorf("无法执行 adb：%w", runErr)
}

// parseDevices 解析 `adb devices` 输出：跳过表头，每行取前两列。
func parseDevices(output string) map[string]string {
	devices := make(map[string]string)
	lines := strings.Split(output, "\n")
	for _, line := range lines[1:] { // 跳过 "List of devices attached"
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			devices[fields[0]] = fields[1]
		}
	}
	return devices
}

// ensureDevice 确保 serial 在 adb devices 中且状态为 device，必要时先 connect。
func ensureDevice(adb string, log func(string)) error {
	list := func() (map[string]string, error) {
		stdout, stderr, code, err := runADB(adb, adbTimeout, "devices")
		if err != nil {
			return nil, err
		}
		if code != 0 {
			return nil, fmt.Errorf("adb devices 执行失败：%s", strings.TrimSpace(string(stdout)+string(stderr)))
		}
		return parseDevices(string(stdout) + string(stderr)), nil
	}

	devices, err := list()
	if err != nil {
		return err
	}
	if devices[serial] == "device" {
		return nil
	}

	stdout, stderr, _, err := runADB(adb, adbTimeout, "connect", serial)
	if err != nil {
		return err
	}
	if msg := strings.TrimSpace(string(stdout) + string(stderr)); msg != "" {
		log(msg)
	}

	devices, err = list()
	if err != nil {
		return err
	}
	switch state := devices[serial]; state {
	case "device":
		return nil
	case "":
		return fmt.Errorf("连接失败：%s 未出现在 adb devices 中。请确认模拟器已启动且 %d 端口可连", serial, port)
	default:
		return fmt.Errorf("设备 %s 当前状态为 %s，无法截屏（unauthorized 需在设备上允许调试授权）", serial, state)
	}
}

// capturePNG 截屏并校验 PNG 签名。
func capturePNG(adb string) ([]byte, error) {
	stdout, stderr, code, err := runADB(adb, captureTimeout, "-s", serial, "exec-out", "screencap", "-p")
	if err != nil {
		return nil, err
	}
	if code == 0 && bytes.HasPrefix(stdout, pngSignature) {
		return stdout, nil
	}
	detail := strings.TrimSpace(string(stderr))
	if detail == "" {
		detail = string(bytes.TrimSpace(stdout[:min(len(stdout), 200)]))
	}
	return nil, fmt.Errorf("截屏失败：adb exec-out screencap 未返回 PNG（返回码 %d）。可能是 adb 版本过旧或设备侧无权限。%s", code, detail)
}

// inRange 三通道全部落在各自闭区间内才算在范围内。
func inRange(r, g, b int) bool {
	return rangeRLow <= r && r <= rangeRHigh &&
		rangeGLow <= g && g <= rangeGHigh &&
		rangeBLow <= b && b <= rangeBHigh
}

// worker 轮询工作状态。
type worker struct {
	log   func(string) // 日志输出（会阻塞，由 UI 侧 channel 消费）
	pause *pauseCtl    // 暂停控制
}

// run 执行完整轮询循环，返回统计与致命错误（nil 表示正常结束）。
func (w *worker) run() (checks, outside, taps int, err error) {
	adb, err := adbPath()
	if err != nil {
		return 0, 0, 0, err
	}
	if err := ensureDevice(adb, w.log); err != nil {
		return 0, 0, 0, err
	}

	side := 2*radius + 1
	w.log(fmt.Sprintf("设备：%s", serial))
	w.log(fmt.Sprintf("中心 (%d, %d)，半径 %d -> 区域 %dx%d 像素", centerX, centerY, radius, side, side))
	w.log(fmt.Sprintf("范围：R %d~%d, G %d~%d, B %d~%d", rangeRLow, rangeRHigh, rangeGLow, rangeGHigh, rangeBLow, rangeBHigh))
	w.log(fmt.Sprintf("间隔：%gs", interval.Seconds()))
	w.log("开始轮询")

	wanted := [4]int{centerX - radius, centerY - radius, centerX + radius, centerY + radius}
	clampWarned := false

	for {
		w.pause.waitIfPaused()

		started := time.Now()
		data, err := capturePNG(adb)
		if err != nil {
			return checks, outside, taps, err
		}

		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return checks, outside, taps, fmt.Errorf("PNG 解码失败：%w", err)
		}
		bounds := img.Bounds()
		width, height := bounds.Dx(), bounds.Dy()

		left, top := max(centerX-radius, 0), max(centerY-radius, 0)
		right, bottom := min(centerX+radius, width-1), min(centerY+radius, height-1)
		if left > right || top > bottom {
			return checks, outside, taps, fmt.Errorf("裁剪区域超出画面：中心 (%d, %d)、半径 %d 与画面 %dx%d 无交集", centerX, centerY, radius, width, height)
		}
		if (left != wanted[0] || top != wanted[1] || right != wanted[2] || bottom != wanted[3]) && !clampWarned {
			w.log(fmt.Sprintf("注意：请求区域 %v 超出画面 %dx%d，已收进画面内为 (%d, %d, %d, %d)", wanted, width, height, left, top, right, bottom))
			clampWarned = true
		}

		// 逐通道算术平均，向下取整（坐标含两端）
		var sumR, sumG, sumB, n int
		for y := top; y <= bottom; y++ {
			for x := left; x <= right; x++ {
				r16, g16, b16, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
				sumR += int(r16 >> 8)
				sumG += int(g16 >> 8)
				sumB += int(b16 >> 8)
				n++
			}
		}
		red, green, blue := sumR/n, sumG/n, sumB/n

		checks++
		stamp := time.Now().Format("15:04:05")
		sample := fmt.Sprintf("RGB=(%3d, %3d, %3d)", red, green, blue)

		if inRange(red, green, blue) {
			w.log(fmt.Sprintf("[%s] %s 在范围内", stamp, sample))
		} else {
			outside++
			_, stderr, code, err := runADB(adb, tapTimeout, "-s", serial, "shell", "input", "tap",
				fmt.Sprint(centerX), fmt.Sprint(centerY))
			if err != nil {
				w.log(fmt.Sprintf("[%s] %s 超出范围 -> 点击失败：%s", stamp, sample, err))
			} else if code == 0 {
				taps++
				w.log(fmt.Sprintf("[%s] %s 超出范围 -> 已点击 (%d, %d)", stamp, sample, centerX, centerY))
			} else {
				detail := strings.TrimSpace(string(stderr))
				w.log(fmt.Sprintf("[%s] %s 超出范围 -> 点击失败（返回码 %d）：%s", stamp, sample, code, detail))
			}
		}

		// 节拍对齐：每轮耗时固定为 interval
		if remaining := interval - time.Since(started); remaining > 0 {
			time.Sleep(remaining)
		}
	}
}
