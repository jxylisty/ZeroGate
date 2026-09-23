// Provision 面板自助添加 zcode 账号：分配端口 → 创建实例 → 启动 zcode-proxy
// → 发起 auth login（浏览器授权）→ 凭证就绪后生成 ZeroGate auths 占位账号。
// 凭证实际存于实例 creds 目录（加密），ZeroGate 侧只记 bridgeUrl 路由。
package zcode

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"zerogate/internal/platform"
)

// Provision 实例编排器（面板自助加号用）。
type Provision struct {
	exePath      string // zcode-proxy.exe 绝对路径
	instancesDir string // 实例根目录（instances/）
	authDir      string // ZeroGate auths 目录（生成占位账号）
	basePort     int    // 端口分配起点（18080 起，跳过已占用）

	mu  sync.Mutex
	cur *ProvisionState
}

// ProvisionState 一次进行中的登录编排。
type ProvisionState struct {
	Name     string // acc-<短id>
	Port     int
	Provider string // zai | bigmodel
	credsDir string

	serveCmd  *exec.Cmd
	loginCmd  *exec.Cmd
	authURL   string
	doneCh    chan struct{}
	doneErr   error
	finalized bool
}

// NewProvision 构造编排器；参数缺失时用默认路径（AGENTS.md 7.6 目录约定）。
func NewProvision(exePath, instancesDir, authDir string, basePort int) *Provision {
	if exePath == "" {
		exePath = "D:/wild-work/research/zcode-proxy/zcode-proxy.exe"
	}
	if instancesDir == "" {
		instancesDir = "D:/wild-work/research/zcode-proxy/instances"
	}
	if basePort == 0 {
		basePort = 18086
	}
	return &Provision{exePath: exePath, instancesDir: instancesDir, authDir: authDir, basePort: basePort}
}

// Busy 是否已有进行中的编排。
func (p *Provision) Busy() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cur != nil
}

// Start 分配端口并启动实例 + 发起 auth login，返回授权 URL（浏览器打开）。
// 成功后调用方应启动 goroutine 等待 Done()。
func (p *Provision) Start(provider string) (string, error) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "zai"
	}
	if provider != "zai" && provider != "bigmodel" {
		return "", fmt.Errorf("zcode provider 仅支持 zai / bigmodel（got %s）", provider)
	}
	p.mu.Lock()
	if p.cur != nil {
		p.mu.Unlock()
		return "", fmt.Errorf("已有 zcode 登录流程进行中")
	}
	port, err := freePort(p.basePort)
	if err != nil {
		p.mu.Unlock()
		return "", err
	}
	name := fmt.Sprintf("acc-logn%04d", time.Now().UnixNano()%10000)
	accDir := filepath.Join(p.instancesDir, name)
	credsDir := filepath.Join(accDir, "creds")
	if err := os.MkdirAll(credsDir, 0o755); err != nil {
		p.mu.Unlock()
		return "", err
	}
	st := &ProvisionState{Name: name, Port: port, Provider: provider, credsDir: credsDir, doneCh: make(chan struct{})}
	if err := writeInstanceFiles(accDir, port, provider, p.exePath); err != nil {
		p.mu.Unlock()
		return "", err
	}
	p.cur = st
	p.mu.Unlock()

	// 启动服务实例（常驻，DATA 目录独立）
	st.serveCmd = exec.Command(p.exePath, "--cli", "serve", filepath.Join(accDir, "config.yaml"))
	st.serveCmd.Dir = filepath.Dir(p.exePath)
	platform.HideWindow(st.serveCmd)
	st.serveCmd.Env = append(os.Environ(),
		"ZCODE_CREDENTIALS_DIR="+credsDir,
		"ZCODE_CLAIM_ENABLED=false",
		"HTTPS_PROXY=", "HTTP_PROXY=", "ALL_PROXY=",
	)
	logFile, err := os.Create(filepath.Join(accDir, "serve.log"))
	if err == nil {
		st.serveCmd.Stdout = logFile
		st.serveCmd.Stderr = logFile
	}
	if err := st.serveCmd.Start(); err != nil {
		p.abort(st, err)
		return "", fmt.Errorf("启动 zcode-proxy 实例失败: %v", err)
	}

	// 发起 auth login（后台），从 stdout 解析授权 URL
	st.loginCmd = exec.Command(p.exePath, "auth", "login", provider)
	st.loginCmd.Dir = filepath.Dir(p.exePath)
	platform.HideWindow(st.loginCmd)
	st.loginCmd.Env = append(os.Environ(),
		"ZCODE_CREDENTIALS_DIR="+credsDir,
		"HTTPS_PROXY=", "HTTP_PROXY=", "ALL_PROXY=",
	)
	loginOut, err := st.loginCmd.StdoutPipe()
	if err != nil {
		p.abort(st, err)
		return "", err
	}
	stderrFile, _ := os.Create(filepath.Join(accDir, "login.log"))
	st.loginCmd.Stderr = stderrFile
	if err := st.loginCmd.Start(); err != nil {
		p.abort(st, err)
		return "", fmt.Errorf("发起 zcode 登录失败: %v", err)
	}
	go func() {
		sc := bufio.NewScanner(loginOut)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "http") {
				p.mu.Lock()
				if st.authURL == "" && (strings.Contains(line, "authorize") || strings.Contains(line, "login")) {
					st.authURL = line
				}
				p.mu.Unlock()
				if st.authURL != "" {
					break
				}
			}
		}
		// 登录进程退出（无论成功失败）→ 标记 done
		_ = st.loginCmd.Wait()
		p.mu.Lock()
		if st.authURL == "" {
			st.doneErr = fmt.Errorf("auth login 未输出授权 URL")
		}
		p.mu.Unlock()
		close(st.doneCh)
	}()
	// 等待 URL 解析（最长 20s）
	deadline := time.Now().Add(20 * time.Second)
	for {
		p.mu.Lock()
		u := st.authURL
		p.mu.Unlock()
		if u != "" {
			return u, nil
		}
		if time.Now().After(deadline) {
			p.abort(st, fmt.Errorf("等待授权 URL 超时"))
			return "", fmt.Errorf("等待授权 URL 超时（登录进程可能异常，看实例 login.log）")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Done 等待登录完成：凭证文件出现 = 成功；login 进程失败 = 失败。
// 完成（含失败）后自动清理编排状态并返回结果；成功需要调用方 Finalize。
func (p *Provision) Done(timeout time.Duration) (ok bool, err error) {
	p.mu.Lock()
	st := p.cur
	p.mu.Unlock()
	if st == nil {
		return false, fmt.Errorf("无进行中的 zcode 登录")
	}
	select {
	case <-st.doneCh:
	case <-time.After(timeout):
		p.abort(st, fmt.Errorf("等待登录超时（%s）", timeout))
		return false, fmt.Errorf("登录超时，实例已保留（实例目录 instances/%s 可删）", st.Name)
	}
	p.mu.Lock()
	err = st.doneErr
	p.cur = nil
	p.mu.Unlock()
	if err != nil {
		return false, err
	}
	// 凭证文件出现才认为成功
	credFile := filepath.Join(st.credsDir, "credentials.json")
	if _, serr := os.Stat(credFile); serr != nil {
		return false, fmt.Errorf("凭证文件未生成（%s）", credFile)
	}
	return true, nil
}

// Finalize 生成 ZeroGate auths 占位账号并返回其文件路径。
func (p *Provision) Finalize() (string, error) {
	p.mu.Lock()
	st := p.cur
	p.mu.Unlock()
	if st == nil {
		return "", fmt.Errorf("无进行中的 zcode 登录")
	}
	if st.finalized {
		return "", fmt.Errorf("账号已落库")
	}
	st.finalized = true
	authFile := filepath.Join(p.authDir, fmt.Sprintf("zcode-%s.json", st.Name))
	content := fmt.Sprintf(`{
  "accessToken": "bridge.header.sig",
  "uid": "zcode-%s",
  "nickname": "ZCode-%s",
  "bridgeUrl": "http://127.0.0.1:%d"
}
`, st.Name, st.Name, st.Port)
	if err := os.WriteFile(authFile, []byte(content), 0o644); err != nil {
		return "", err
	}
	return authFile, nil
}

// Abort 取消进行中的编排（保留实例目录和 serve 进程，方便排查/复用凭证）。
func (p *Provision) Abort() error {
	p.mu.Lock()
	st := p.cur
	p.mu.Unlock()
	if st == nil {
		return fmt.Errorf("无进行中的 zcode 登录")
	}
	if st.loginCmd != nil && st.loginCmd.Process != nil {
		_ = st.loginCmd.Process.Kill()
	}
	p.mu.Lock()
	p.cur = nil
	p.mu.Unlock()
	return nil
}

func (p *Provision) abort(st *ProvisionState, err error) {
	if st.loginCmd != nil && st.loginCmd.Process != nil {
		_ = st.loginCmd.Process.Kill()
	}
	p.mu.Lock()
	if p.cur == st {
		p.cur = nil
	}
	p.mu.Unlock()
	log.Printf("zcode provision abort %s: %v", st.Name, err)
}

// freePort 从 base 起找第一个可监听的端口。
func freePort(base int) (int, error) {
	for port := base; port < base+200; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = ln.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("端口 %d-%d 全部被占用", base, base+200)
}

// writeInstanceFiles 写实例 config.yaml + start.sh。
func writeInstanceFiles(accDir string, port int, provider, exePath string) error {
	cfg := fmt.Sprintf(`server:
  port: %d
  host: "127.0.0.1"

auth:
  proxyApiKey: "sk-04cEYoXf3mThKD0El6zG9jyrfc4YgZH4"

provider: %s
plan: start-plan

defaultModel: glm-5.3-flash

models:
  - glm-5.3
  - glm-5.3-flash
`, port, provider)
	if err := os.WriteFile(filepath.Join(accDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		return err
	}
	sh := fmt.Sprintf(`#!/usr/bin/env bash
export ZCODE_CREDENTIALS_DIR="$(dirname "$0")/creds"
export ZCODE_CLAIM_ENABLED=false
export HTTPS_PROXY= HTTP_PROXY= ALL_PROXY=
cd "%s"
exec ./zcode-proxy.exe --cli serve "%s/config.yaml"
`, filepath.Dir(exePath), accDir)
	return os.WriteFile(filepath.Join(accDir, "start.sh"), []byte(sh), 0o755)
}
