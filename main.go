package main

// 极简 GUI：一个「开始/暂停」按钮 + 滚动日志。
// Fyne 2.6+ 线程模型：后台 goroutine 不得直接操作 UI 控件，
// 必须通过 fyne.Do 提交到主线程执行。
// worker goroutine 通过 channel 推日志，由专门的 UI goroutine
// 消费并通过 fyne.Do 刷新文本框。

import (
	"fmt"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// pauseCtl 运行/暂停控制：pause 建立阻塞通道，resume 关闭它。
type pauseCtl struct {
	mu sync.Mutex
	ch chan struct{} // 非 nil 表示处于暂停态
}

func (p *pauseCtl) pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ch == nil {
		p.ch = make(chan struct{})
	}
}

func (p *pauseCtl) resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ch != nil {
		close(p.ch)
		p.ch = nil
	}
}

// waitIfPaused 暂停时阻塞，直到被 resume。
func (p *pauseCtl) waitIfPaused() {
	p.mu.Lock()
	ch := p.ch
	p.mu.Unlock()
	if ch != nil {
		<-ch
	}
}

const maxLogBytes = 256 * 1024 // 日志过长时截尾，防止内存膨胀

func main() {
	a := app.New()
	w := a.NewWindow("auto_click")
	w.Resize(fyne.NewSize(560, 420))

	logCh := make(chan string, 256)

	entry := widget.NewRichText(&widget.TextSegment{Style: widget.RichTextStyleParagraph})
	entry.Wrapping = fyne.TextWrapWord
	scroll := container.NewVScroll(entry)

	var mu sync.Mutex
	var running, paused bool
	var ctl *pauseCtl

	var btn *widget.Button
	btn = widget.NewButton("开始", func() {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case !running:
			running, paused = true, false
			ctl = &pauseCtl{}
			btn.SetText("暂停")
			go func(c *pauseCtl) {
				wk := &worker{log: func(s string) { logCh <- s }, pause: c}
				checks, outside, taps, err := wk.run()

				mu.Lock()
				running = false
				fyne.Do(func() { btn.SetText("开始") })
				mu.Unlock()

				if err != nil {
					logCh <- "运行中断：" + err.Error()
				}
				logCh <- fmt.Sprintf("结束：共检查 %d 次，其中 %d 次超出范围，实际点击 %d 次", checks, outside, taps)
			}(ctl)
		case !paused:
			paused = true
			ctl.pause()
			btn.SetText("继续")
		default:
			paused = false
			ctl.resume()
			btn.SetText("暂停")
		}
	})

	w.SetContent(container.NewBorder(btn, nil, nil, nil, scroll))

	// 日志消费：刷新文本框并滚到底部（fyne.Do 提交到主线程）
	go func() {
		var builder strings.Builder
		seg, _ := entry.Segments[0].(*widget.TextSegment)
		for line := range logCh {
			builder.WriteString(line)
			builder.WriteByte('\n')
			text := builder.String()
			if len(text) > maxLogBytes {
				text = text[len(text)-maxLogBytes:]
				builder.Reset()
				builder.WriteString(text)
			}
			t := text // fyne.Do 异步执行，拷贝当前值避免闭包捕获被后续迭代覆盖
			fyne.Do(func() {
				seg.Text = t
				entry.Refresh()
				scroll.ScrollToBottom()
			})
		}
	}()

	w.ShowAndRun()
}
