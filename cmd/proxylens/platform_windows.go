package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/gogpu/systray"
	"golang.org/x/sys/windows"

	"github.com/yaodao0yaodao/proxylens/internal/config"
)

var (
	shell32        = windows.NewLazySystemDLL("shell32.dll")
	shellExecuteW  = shell32.NewProc("ShellExecuteW")
	removeTrayOnce sync.Once
)

func runPlatform(ctx context.Context, cancel context.CancelFunc, server *http.Server, cfg config.Config, log *slog.Logger) error {
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	url := localManagementURL(cfg.Listen)
	if err := waitForManagementServer(ctx, url, done); err != nil {
		cancel()
		return err
	}

	tray := systray.New()
	removeTray := func() { removeTrayOnce.Do(tray.Remove) }
	show := func() {
		if err := openBrowser(url); err != nil {
			log.Warn("open browser", "error", err)
			tray.ShowNotification("ProxyLens", "无法打开浏览器："+err.Error())
		}
	}
	menu := systray.NewMenu()
	menu.Add("显示软件", show)
	portDialog := make(chan struct{}, 1)
	menu.Add("修改端口", func() {
		select {
		case portDialog <- struct{}{}:
			go func() {
				defer func() { <-portDialog }()
				port, ok, err := promptPort(portFromListen(cfg.Listen))
				if err != nil {
					tray.ShowNotification("ProxyLens", "修改端口失败："+err.Error())
					return
				}
				if !ok || port == portFromListen(cfg.Listen) {
					return
				}
				listener, listenErr := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
				if listenErr != nil {
					tray.ShowNotification("ProxyLens", fmt.Sprintf("端口 %d 已被占用", port))
					return
				}
				_ = listener.Close()
				if err = savePort(cfg.DataDir, port); err != nil {
					tray.ShowNotification("ProxyLens", "保存端口失败："+err.Error())
					return
				}
				if err = restartSelf(portFromListen(cfg.Listen)); err != nil {
					log.Error("restart after port change", "error", err)
					tray.ShowNotification("ProxyLens", "重启失败，请手动重新打开软件")
					return
				}
				cancel()
				removeTray()
			}()
		default:
			tray.ShowNotification("ProxyLens", "端口设置窗口已经打开")
		}
	})
	menu.AddSeparator()
	menu.Add("退出", func() { cancel(); removeTray() })
	tray.SetIcon(trayIcon()).SetTooltip("ProxyLens").SetMenu(menu).OnDoubleClick(show).Show()

	marker := filepath.Join(cfg.DataDir, "windows-started")
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		if err = os.MkdirAll(cfg.DataDir, 0700); err == nil {
			if err = os.WriteFile(marker, []byte(time.Now().Format(time.RFC3339)), 0600); err == nil {
				if os.Getenv("PROXYLENS_NO_AUTO_OPEN") != "1" {
					show()
				}
			}
		}
	}
	go func() {
		select {
		case <-ctx.Done():
			removeTray()
		case err := <-done:
			if err != nil && !strings.Contains(err.Error(), "Server closed") {
				log.Error("HTTP server stopped", "error", err)
			}
			cancel()
			removeTray()
		}
	}()
	return tray.Run()
}

func localManagementURL(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://127.0.0.1:9099/"
	}
	return "http://127.0.0.1:" + port + "/"
}

func portFromListen(listen string) int {
	_, raw, err := net.SplitHostPort(listen)
	if err == nil {
		if value, parseErr := strconv.Atoi(raw); parseErr == nil {
			return value
		}
	}
	return 9099
}

func waitForManagementServer(ctx context.Context, url string, done <-chan error) error {
	client := &http.Client{Timeout: time.Second}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		response, err := client.Get(url + "healthz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case err = <-done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("Web 服务启动超时")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func openBrowser(url string) error {
	action, _ := windows.UTF16PtrFromString("open")
	target, _ := windows.UTF16PtrFromString(url)
	result, _, callErr := shellExecuteW.Call(0, uintptr(unsafe.Pointer(action)), uintptr(unsafe.Pointer(target)), 0, 0, 1)
	if result <= 32 {
		return callErr
	}
	return nil
}

func promptPort(current int) (int, bool, error) {
	script := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; $f=New-Object System.Windows.Forms.Form; $f.Text='ProxyLens 修改端口'; $f.ClientSize=New-Object System.Drawing.Size(360,135); $f.StartPosition='CenterScreen'; $f.TopMost=$true; $f.ShowInTaskbar=$false; $l=New-Object System.Windows.Forms.Label; $l.Text='请输入新的 Web 管理端口（1-65535）'; $l.AutoSize=$true; $l.Location=New-Object System.Drawing.Point(18,18); $t=New-Object System.Windows.Forms.TextBox; $t.Text='%d'; $t.Location=New-Object System.Drawing.Point(18,48); $t.Width=324; $ok=New-Object System.Windows.Forms.Button; $ok.Text='确定'; $ok.DialogResult=[System.Windows.Forms.DialogResult]::OK; $ok.Location=New-Object System.Drawing.Point(186,88); $cancel=New-Object System.Windows.Forms.Button; $cancel.Text='取消'; $cancel.DialogResult=[System.Windows.Forms.DialogResult]::Cancel; $cancel.Location=New-Object System.Drawing.Point(267,88); $f.Controls.AddRange(@($l,$t,$ok,$cancel)); $f.AcceptButton=$ok; $f.CancelButton=$cancel; $f.Add_Shown({$t.SelectAll();$t.Focus()}); if($f.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK){[Console]::Out.Write($t.Text)}; $f.Dispose()`, current)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", script)
	cmd.SysProcAttr = hiddenProcessAttributes()
	output, err := cmd.Output()
	if err != nil {
		return 0, false, err
	}
	raw := strings.TrimSpace(string(output))
	if raw == "" {
		return 0, false, nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, false, errors.New("端口必须是 1-65535 之间的整数")
	}
	return port, true, nil
}

func savePort(dataDir string, port int) error {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dataDir, "web-port")
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(strconv.Itoa(port)+"\n"), 0600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func restartSelf(oldPort int) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, "--restart-after-port", strconv.Itoa(oldPort))
	cmd.Dir = filepath.Dir(executable)
	cmd.SysProcAttr = hiddenProcessAttributes()
	return cmd.Start()
}

func runRestartHelper() bool {
	if len(os.Args) != 3 || os.Args[1] != "--restart-after-port" {
		return false
	}
	port, err := strconv.Atoi(os.Args[2])
	if err != nil || port < 1 || port > 65535 {
		return true
	}
	address := "127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		connection, dialErr := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if dialErr != nil {
			break
		}
		_ = connection.Close()
		time.Sleep(100 * time.Millisecond)
	}
	executable, err := os.Executable()
	if err == nil {
		cmd := exec.Command(executable)
		cmd.Dir = filepath.Dir(executable)
		cmd.SysProcAttr = hiddenProcessAttributes()
		_ = cmd.Start()
	}
	return true
}

func hiddenProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
}

func trayIcon() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	blue := color.NRGBA{R: 79, G: 99, B: 220, A: 255}
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	for y := 1; y < 31; y++ {
		for x := 1; x < 31; x++ {
			if dx, dy := x-16, y-16; dx*dx+dy*dy <= 225 {
				img.SetNRGBA(x, y, blue)
			}
		}
	}
	for y := 8; y <= 23; y++ {
		for x := 9; x <= 12; x++ {
			img.SetNRGBA(x, y, white)
		}
	}
	for y := 8; y <= 11; y++ {
		for x := 12; x <= 21; x++ {
			img.SetNRGBA(x, y, white)
		}
	}
	for y := 12; y <= 17; y++ {
		for x := 18; x <= 21; x++ {
			img.SetNRGBA(x, y, white)
		}
	}
	for y := 18; y <= 21; y++ {
		for x := 12; x <= 21; x++ {
			img.SetNRGBA(x, y, white)
		}
	}
	var buffer bytes.Buffer
	_ = png.Encode(&buffer, img)
	return buffer.Bytes()
}
