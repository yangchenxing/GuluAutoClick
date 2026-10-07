# GuluAutoClick

一念逍遥「域外古路」自动点击辅助工具：周期性截取模拟器画面，检测指定区域的平均颜色，颜色越界时自动点击。

## 使用方法

### 依赖条件

1. 使用 **MuMu 模拟器** 运行《一念逍遥》。
2. 模拟器分辨率设置为 **1080 x 1920**（竖屏）。
3. 打开模拟器的 **ADB 调试，端口 5555**（本工具固定连接 `127.0.0.1:5555`）。

### 运行

`dist/` 目录下是已编译好的可执行文件，**adb 已放在同一目录，无需额外安装**：

- **macOS**：运行 `dist/mac/auto_click`
- **Windows**：运行 `dist/windows/auto_click.exe`

直接双击或在终端运行即可。**使用前需要先打开游戏，进入「域外古路」画面，再启动程序并点击「开始」。**

程序启动后会：

1. 自动执行 `adb connect 127.0.0.1:5555` 并检查设备状态；
2. 每 2 秒截屏一次，计算中心点 (844, 1632) 附近 65x65 像素区域的平均 RGB；
3. 当 RGB 不在设定范围内时，自动点击 (844, 1632)；
4. 点击参数（中心、半径、颜色范围、间隔）在 `worker.go` 顶部硬编码，如需调整请修改后重新编译。

> ⚠️ **注意**：如果要离开「域外古路」画面，需要先点击「暂停」或关闭程序，否则程序会持续点击，造成意外后果。

### 界面操作

- 点击「开始」启动轮询，按钮变为「暂停」；
- 点击「暂停」可随时暂停，再点「继续」恢复；
- 日志区实时显示每次检测的 RGB 值与点击记录。

## 项目结构

```
GuluAutoClick/
├── main.go            # GUI 主程序（Fyne）：开始/暂停按钮 + 滚动日志，fyne.Do 线程模型
├── worker.go          # 轮询核心：adb 截屏 -> 裁剪区域 -> RGB 均值 -> 越界点击
├── go.mod / go.sum    # Go 模块依赖
├── build.sh           # 一键构建脚本：编译 mac / windows 两个目标，并拷贝 adb 到产物目录
├── auto_click.md      # 设计说明文档
├── adb-mac/           # macOS 版 adb 可执行文件
├── adb-windows/       # Windows 版 adb.exe 及配套 DLL
└── dist/              # 编译产物（可直接分发）
    ├── mac/
    │   ├── auto_click
    │   └── adb
    └── windows/
        ├── auto_click.exe
        ├── adb.exe
        ├── AdbWinApi.dll
        └── AdbWinUsbApi.dll
```

## 自行编译

需要 Go 1.21+；编译 Windows 版本还需 mingw-w64（`brew install mingw-w64`）。运行：

```sh
./build.sh
```

产物输出到 `dist/mac/` 与 `dist/windows/`，adb 会自动复制到可执行文件同目录。
