#!/bin/sh
# 一键构建：mac 与 windows 两个发布目录，adb 自动放到 exe 同目录
set -e
cd "$(dirname "$0")"

# macOS
go build -o dist/mac/auto_click .
cp adb-mac/adb dist/mac/

# Windows（需要 mingw-w64：brew install mingw-w64）
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
    go build -o dist/windows/auto_click.exe .
cp adb-windows/adb.exe adb-windows/AdbWinApi.dll adb-windows/AdbWinUsbApi.dll dist/windows/

echo "完成："
ls -lh dist/mac dist/windows
