// Package systray 系统托盘封装：固定菜单（打开主界面 / 查看日志 / 退出）。
// 基于 energye/systray（跨平台 Windows/macOS/Linux），单文件实现，无平台差异代码。
//
// 设计约定（见项目 AGENTS.md R1-R3）：
//   - 菜单固定，不做动态内容，不用定时/事件刷新
//   - 单击/双击托盘 = 打开主界面
package systray

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"log"

	"github.com/energye/systray"
)

// Actions 托盘动作回调（由 daemon 注入）。
type Actions struct {
	// OpenUI 打开主界面（系统浏览器）。
	OpenUI func()
	// OpenLog 打开日志文件。
	OpenLog func()
	// Quit 退出程序。
	Quit func()
}

// miniPNG 生成 16x16 纯色 PNG（RGBA）。
//
// ⚠️ 仅用于 macOS/Linux 的托盘图标。Windows 的 LoadImage(IMAGE_ICON) 只认 ICO
// 格式，喂 PNG 会失败并打印 "unable to load icon from temp file"（表现为菜单项
// 图标静默丢失）。Windows 菜单图标一律用 miniICO 生成。
func miniPNG(r, g, b uint8) []byte {
	const w, h = 16, 16
	var buf bytes.Buffer

	// PNG signature
	buf.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10})

	// IHDR
	writeChunk(&buf, "IHDR", func() {
		binary.Write(&buf, binary.BigEndian, int32(w))
		binary.Write(&buf, binary.BigEndian, int32(h))
		buf.WriteByte(8) // bit depth
		buf.WriteByte(6) // color type RGBA
		buf.WriteByte(0) // compression
		buf.WriteByte(0) // filter
		buf.WriteByte(0) // interlace
	})

	// IDAT
	writeChunk(&buf, "IDAT", func() {
		// zlib header
		buf.Write([]byte{0x78, 0x01})
		adler := adler32Start()
		for y := 0; y < h; y++ {
			buf.WriteByte(0) // filter none
			row := []byte{r, g, b, 255}
			for x := 0; x < w; x++ {
				buf.Write(row)
			}
			adler = adler32Update(adler, []byte{0}) // filter byte
			for x := 0; x < w; x++ {
				adler = adler32Update(adler, row)
			}
		}
		// adler32
		binary.Write(&buf, binary.BigEndian, adler)
	})

	// IEND
	writeChunk(&buf, "IEND", nil)

	return buf.Bytes()
}

// bmp16Frame 构造 ICO 内嵌的 16x16 32bpp BGRA 位图帧（含 AND 掩码）。
//
// ICO 内嵌位图与独立 .bmp 不同：自下而上存储，且末尾必须带 1bpp AND 掩码
// （每行按 4 字节对齐，16 像素 = 2 字节 → 补齐到 4 字节）。缺掩码时
// LoadImage 会拒绝加载，图标再次丢失。
func bmp16Frame(r, g, b uint8, border bool) []byte {
	const w, h = 16, 16
	var buf bytes.Buffer

	// BITMAPINFOHEADER：高度记为 2×16（XOR 图 + AND 掩码）
	binary.Write(&buf, binary.LittleEndian, uint32(40))          // biSize
	binary.Write(&buf, binary.LittleEndian, int32(w))            // biWidth
	binary.Write(&buf, binary.LittleEndian, int32(h*2))          // biHeight
	binary.Write(&buf, binary.LittleEndian, uint16(1))           // biPlanes
	binary.Write(&buf, binary.LittleEndian, uint16(32))          // biBitCount
	binary.Write(&buf, binary.LittleEndian, uint32(0))           // biCompression = BI_RGB
	binary.Write(&buf, binary.LittleEndian, uint32(w*h*4))       // biSizeImage
	binary.Write(&buf, binary.LittleEndian, int32(0))            // biXPelsPerMeter
	binary.Write(&buf, binary.LittleEndian, int32(0))            // biYPelsPerMeter
	binary.Write(&buf, binary.LittleEndian, uint32(0))           // biClrUsed
	binary.Write(&buf, binary.LittleEndian, uint32(0))           // biClrImportant

	// XOR 位图：自下而上，BGRA
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			isBorder := border && (y == 0 || y == h-1 || x == 0 || x == w-1)
			if isBorder {
				buf.Write([]byte{255, 255, 255, 255}) // 白边
			} else {
				buf.Write([]byte{b, g, r, 255}) // BGRA
			}
		}
	}

	// AND 掩码：32bpp 下全 0 表示"全部由 alpha 决定"，每行 4 字节对齐
	rowBytes := ((w + 31) / 32) * 4
	for y := 0; y < h; y++ {
		buf.Write(make([]byte, rowBytes))
	}
	return buf.Bytes()
}

// miniICO 生成 16x16 纯色 ICO（Windows 菜单项图标）。
func miniICO(r, g, b uint8, border bool) []byte {
	data := bmp16Frame(r, g, b, border)
	var buf bytes.Buffer

	binary.Write(&buf, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // type = icon
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // count
	buf.WriteByte(16)                                  // width
	buf.WriteByte(16)                                  // height
	buf.WriteByte(0)                                   // palette
	buf.WriteByte(0)                                   // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1))  // planes
	binary.Write(&buf, binary.LittleEndian, uint16(32)) // bitcount
	binary.Write(&buf, binary.LittleEndian, uint32(len(data)))
	binary.Write(&buf, binary.LittleEndian, uint32(22)) // 偏移 = 6 + 16×1
	buf.Write(data)

	return buf.Bytes()
}

func writeChunk(buf *bytes.Buffer, name string, writeData func()) {
	var data bytes.Buffer
	if writeData != nil {
		writeData()
	}
	binary.Write(buf, binary.BigEndian, uint32(data.Len()))
	start := buf.Len()
	buf.Write([]byte(name))
	buf.Write(data.Bytes())
	crc := crc32.ChecksumIEEE(buf.Bytes()[start:])
	binary.Write(buf, binary.BigEndian, crc)
}

func adler32Start() uint32 { return 1 }
func adler32Update(a uint32, data []byte) uint32 {
	s1, s2 := a&0xffff, a>>16
	for _, b := range data {
		s1 = (s1 + uint32(b)) % 65521
		s2 = (s2 + s1) % 65521
	}
	return s2<<16 | s1
}

// Run 启动托盘（阻塞，直到 Quit）。icon 为 ico/png 字节。
func Run(icon []byte, tooltip string, act Actions) {
	if act.OpenUI == nil {
		act.OpenUI = func() {}
	}
	if act.OpenLog == nil {
		act.OpenLog = func() {}
	}
	if act.Quit == nil {
		act.Quit = func() {}
	}

	// 各菜单项图标（16x16 纯色小方块）。
	// 必须用 ICO：Windows 的 LoadImage(IMAGE_ICON) 不接受 PNG，喂 PNG 会导致
	// 菜单图标静默加载失败（日志出现 "unable to load icon from temp file"）。
	iconOpen := miniICO(37, 99, 235, true)   // 蓝色 - 打开
	iconLog  := miniICO(140, 145, 159, true) // 灰色 - 日志
	iconQuit := miniICO(220, 38, 38, true)   // 红色 - 退出

	systray.Run(func() {
		systray.SetIcon(icon)
		systray.SetTooltip(tooltip)

		mOpen := systray.AddMenuItem("打开主界面", "在系统浏览器中打开管理界面")
		mOpen.SetIcon(iconOpen)

		mLog := systray.AddMenuItem("查看日志", "用系统默认编辑器打开日志文件")
		mLog.SetIcon(iconLog)

		systray.AddSeparator()

		mQuit := systray.AddMenuItem("退出", "退出程序")
		mQuit.SetIcon(iconQuit)

		// 单击 / 双击 = 打开主界面（与旧版行为一致）
		systray.SetOnClick(func(systray.IMenu) { go act.OpenUI() })
		systray.SetOnDClick(func(systray.IMenu) { go act.OpenUI() })

		// 菜单回调：全部 goroutine 化（托盘消息循环线程只做投递，绝不阻塞）
		mOpen.Click(func() { go act.OpenUI() })
		mLog.Click(func() { go act.OpenLog() })
		mQuit.Click(func() { go act.Quit() })
	}, func() {
		log.Printf("托盘已退出")
	})
}