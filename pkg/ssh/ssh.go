package ssh

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
	"github.com/rainbond/rainbond-offline-installer/pkg/config"
)

// SSHKeyPair holds SSH key information
type SSHKeyPair struct {
	PublicKeyPath  string
	PrivateKeyPath string
}

// GenerateSSHKeyPair generates a new SSH key pair
func GenerateSSHKeyPair(forceGenerate bool) (*SSHKeyPair, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("无法获取用户主目录: %w", err)
	}

	sshDir := filepath.Join(homeDir, ".ssh")
	privateKeyPath := filepath.Join(sshDir, "id_rsa")
	publicKeyPath := privateKeyPath + ".pub"

	// 确保 .ssh 目录存在
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return nil, fmt.Errorf("无法创建 .ssh 目录: %w", err)
	}

	// 检查是否需要生成密钥对
	needGenerate := forceGenerate
	if !needGenerate {
		// 只检查公钥是否存在，如果公钥不存在就生成
		if _, err := os.Stat(publicKeyPath); os.IsNotExist(err) {
			needGenerate = true
		}
	}

	if needGenerate {
		// 如果强制生成，先删除现有密钥
		if forceGenerate {
			os.Remove(privateKeyPath)
			os.Remove(publicKeyPath)
		}

		// 生成新的密钥对（固定生成id_rsa）
		fmt.Printf("生成SSH密钥对 id_rsa...\n")
		cmd := exec.Command("ssh-keygen", "-t", "rsa", "-b", "4096", "-f", privateKeyPath, "-N", "")
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("生成SSH密钥失败: %w", err)
		}
		fmt.Printf("SSH密钥对生成成功: %s\n", publicKeyPath)
	} else {
		fmt.Printf("使用现有SSH密钥对: %s\n", publicKeyPath)
	}

	return &SSHKeyPair{
		PublicKeyPath:  publicKeyPath,
		PrivateKeyPath: privateKeyPath,
	}, nil
}

// CopySSHKeyWithSSHCopyID 使用 ssh-copy-id 复制SSH密钥
func CopySSHKeyWithSSHCopyID(keyPair *SSHKeyPair, host config.Host) error {
	fmt.Printf("正在为主机 %s 配置SSH免密登录...\n", host.IP)
	
	// 构建 ssh-copy-id 命令
	args := []string{"-i", keyPair.PublicKeyPath}
	args = append(args, fmt.Sprintf("%s@%s", host.User, host.IP))

	cmd := exec.Command("ssh-copy-id", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// CopySSHKeyWithExpect 使用expect脚本复制SSH密钥
func CopySSHKeyWithExpect(keyPair *SSHKeyPair, host config.Host, password string) error {
	// 检查expect是否可用
	if _, err := exec.LookPath("expect"); err != nil {
		return fmt.Errorf("expect命令不可用，请安装expect或使用ssh-copy-id方式")
	}

	// 创建临时expect脚本
	expectScript := fmt.Sprintf(`#!/usr/bin/expect -f
set timeout 30
spawn ssh-copy-id -i %s %s@%s
expect {
    "Are you sure you want to continue connecting" {
        send "yes\r"
        exp_continue
    }
    "password:" {
        send "%s\r"
    }
    "Password:" {
        send "%s\r"
    }
}
expect eof
`, keyPair.PublicKeyPath, host.User, host.IP, password, password)

	// 写入临时文件
	tmpFile, err := ioutil.TempFile("", "ssh-copy-expect-*.exp")
	if err != nil {
		return fmt.Errorf("创建临时expect脚本失败: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(expectScript); err != nil {
		return fmt.Errorf("写入expect脚本失败: %w", err)
	}
	tmpFile.Close()

	// 设置执行权限
	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		return fmt.Errorf("设置脚本权限失败: %w", err)
	}

	// 执行expect脚本
	fmt.Printf("使用expect脚本为主机 %s 配置SSH免密登录...\n", host.IP)
	cmd := exec.Command("expect", tmpFile.Name())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// TestSSHConnection 测试SSH连接
func TestSSHConnection(host config.Host) error {
	fmt.Printf("测试到主机 %s 的SSH连接...\n", host.IP)
	
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5"}
	args = append(args, fmt.Sprintf("%s@%s", host.User, host.IP), "echo", "SSH连接成功")

	cmd := exec.Command("ssh", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("SSH连接测试失败: %w\n输出: %s", err, string(output))
	}

	fmt.Printf("主机 %s SSH连接测试成功\n", host.IP)
	return nil
}

// PromptForPassword 提示用户输入密码
func PromptForPassword(host config.Host) (string, error) {
	fmt.Printf("请输入主机 %s (%s@%s) 的密码: ", host.IP, host.User, host.IP)
	
	password, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return "", fmt.Errorf("读取密码失败: %w", err)
	}
	fmt.Println() // 换行，因为ReadPassword不会自动换行
	
	return strings.TrimSpace(string(password)), nil
}

// PromptForPasswordSilent 提示用户输入密码（隐藏输入）
func PromptForPasswordSilent() (string, error) {
	password, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return "", fmt.Errorf("读取密码失败: %w", err)
	}
	fmt.Println() // 换行，因为ReadPassword不会自动换行
	
	return strings.TrimSpace(string(password)), nil
}

// SetupSSHMethod 表示SSH设置方法
type SetupSSHMethod int

const (
	MethodSSHCopyID SetupSSHMethod = iota // 使用ssh-copy-id（推荐）
	MethodExpect                          // 使用expect脚本
	MethodNativeGo                        // 使用Go原生SSH客户端
)

// SSHSetupOptions SSH设置选项
type SSHSetupOptions struct {
	Method           SetupSSHMethod
	UnifiedPassword  bool
	ForceGenerate    bool
	Password         string // 用于expect方法
}

// SetupSSHForHosts 为所有主机设置SSH免密登录
func SetupSSHForHosts(hosts []config.Host, options SSHSetupOptions) (*SSHKeyPair, error) {
	// 1. 生成或获取SSH密钥对
	keyPair, err := GenerateSSHKeyPair(options.ForceGenerate)
	if err != nil {
		return nil, fmt.Errorf("SSH密钥对处理失败: %w", err)
	}

	// 2. 为每个主机配置SSH免密登录
	var globalPassword string
	if options.UnifiedPassword {
		// 对于统一密码模式，询问一次密码用于所有主机
		if options.Password != "" {
			globalPassword = options.Password
		} else {
			// 提示用户输入统一密码
			fmt.Printf("请输入所有主机的统一密码: ")
			password, err := PromptForPasswordSilent()
			if err != nil {
				return nil, fmt.Errorf("获取统一密码失败: %w", err)
			}
			globalPassword = password
		}
		fmt.Printf("将使用统一密码配置 %d 台主机\n\n", len(hosts))
	}

	for _, host := range hosts {
		// 测试SSH免密连接是否已经工作
		if isSSHPasswordlessWorking(host) {
			fmt.Printf("主机 %s SSH免密登录已配置，跳过\n", host.IP)
			continue
		}

		var err error
		switch options.Method {
		case MethodSSHCopyID:
			if globalPassword != "" {
				// 如果有统一密码，使用expect方法（如果可用）
				if _, expectErr := exec.LookPath("expect"); expectErr == nil {
					err = CopySSHKeyWithExpect(keyPair, host, globalPassword)
				} else {
					// expect不可用时，提示用户手动输入（保持原有行为）
					fmt.Printf("注意: 统一密码模式需要expect工具，当前使用交互式ssh-copy-id\n")
					err = CopySSHKeyWithSSHCopyID(keyPair, host)
				}
			} else {
				err = CopySSHKeyWithSSHCopyID(keyPair, host)
			}
		case MethodExpect:
			password := globalPassword
			if password == "" {
				password, err = PromptForPassword(host)
				if err != nil {
					return nil, fmt.Errorf("获取主机 %s 密码失败: %w", host.IP, err)
				}
			}
			err = CopySSHKeyWithExpect(keyPair, host, password)
		case MethodNativeGo:
			password := globalPassword
			if password == "" {
				password, err = PromptForPassword(host)
				if err != nil {
					return nil, fmt.Errorf("获取主机 %s 密码失败: %w", host.IP, err)
				}
			}
			err = CopySSHKeyWithNativeGo(keyPair, host, password)
		}

		if err != nil {
			return nil, fmt.Errorf("为主机 %s 配置SSH免密登录失败: %w", host.IP, err)
		}

		// 3. 测试SSH连接
		var testErr error
		switch options.Method {
		case MethodNativeGo:
			testErr = TestSSHConnectionWithKey(host, keyPair)
		default:
			testErr = TestSSHConnection(host)
		}
		
		if testErr != nil {
			return nil, fmt.Errorf("主机 %s SSH连接测试失败: %w", host.IP, testErr)
		}
	}

	return keyPair, nil
}

// CopySSHKeyWithNativeGo 使用Go原生SSH客户端复制SSH密钥
func CopySSHKeyWithNativeGo(keyPair *SSHKeyPair, host config.Host, password string) error {
	fmt.Printf("使用Go原生SSH客户端为主机 %s 配置SSH免密登录...\n", host.IP)

	// 读取公钥内容
	publicKeyData, err := ioutil.ReadFile(keyPair.PublicKeyPath)
	if err != nil {
		return fmt.Errorf("读取公钥文件失败: %w", err)
	}

	// 创建SSH客户端配置
	config := &ssh.ClientConfig{
		User: host.User,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // 注意：生产环境中应该验证主机密钥
		Timeout:         10 * time.Second,
	}

	// 连接SSH
	addr := net.JoinHostPort(host.IP, "22")
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("SSH连接失败: %w", err)
	}
	defer client.Close()

	// 创建SSH会话
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("创建SSH会话失败: %w", err)
	}
	defer session.Close()

	// 构建添加公钥的命令
	publicKeyContent := strings.TrimSpace(string(publicKeyData))
	command := fmt.Sprintf(`
		mkdir -p ~/.ssh
		chmod 700 ~/.ssh
		echo '%s' >> ~/.ssh/authorized_keys
		chmod 600 ~/.ssh/authorized_keys
		# 去重复的公钥
		sort ~/.ssh/authorized_keys | uniq > ~/.ssh/authorized_keys.tmp
		mv ~/.ssh/authorized_keys.tmp ~/.ssh/authorized_keys
	`, publicKeyContent)

	// 执行命令
	output, err := session.CombinedOutput(command)
	if err != nil {
		return fmt.Errorf("执行添加公钥命令失败: %w\n输出: %s", err, string(output))
	}

	fmt.Printf("主机 %s SSH免密配置成功\n", host.IP)
	return nil
}

// TestSSHConnectionWithKey 使用密钥测试SSH连接
func TestSSHConnectionWithKey(host config.Host, keyPair *SSHKeyPair) error {
	fmt.Printf("使用密钥测试到主机 %s 的SSH连接...\n", host.IP)

	// 读取私钥
	privateKeyData, err := ioutil.ReadFile(keyPair.PrivateKeyPath)
	if err != nil {
		return fmt.Errorf("读取私钥文件失败: %w", err)
	}

	// 解析私钥
	signer, err := ssh.ParsePrivateKey(privateKeyData)
	if err != nil {
		return fmt.Errorf("解析私钥失败: %w", err)
	}

	// 创建SSH客户端配置
	config := &ssh.ClientConfig{
		User: host.User,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}

	// 连接SSH
	addr := net.JoinHostPort(host.IP, "22")
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("SSH密钥认证失败: %w", err)
	}
	defer client.Close()

	// 创建会话并测试
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("创建SSH会话失败: %w", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput("echo 'SSH密钥认证成功'")
	if err != nil {
		return fmt.Errorf("执行测试命令失败: %w\n输出: %s", err, string(output))
	}

	if !strings.Contains(string(output), "SSH密钥认证成功") {
		return fmt.Errorf("SSH连接测试返回异常: %s", string(output))
	}

	fmt.Printf("主机 %s SSH密钥连接测试成功\n", host.IP)
	return nil
}

// isSSHPasswordlessWorking 测试SSH免密登录是否已经工作
func isSSHPasswordlessWorking(host config.Host) bool {
	// 构建SSH测试命令，使用BatchMode禁止交互式输入
	args := []string{
		"-o", "BatchMode=yes",           // 禁止交互式输入
		"-o", "ConnectTimeout=5",        // 连接超时5秒
		"-o", "StrictHostKeyChecking=no", // 跳过主机密钥检查
		"-o", "UserKnownHostsFile=/dev/null", // 不使用known_hosts文件
		fmt.Sprintf("%s@%s", host.User, host.IP),
		"echo 'SSH_TEST_SUCCESS'",
	}

	cmd := exec.Command("ssh", args...)
	output, err := cmd.CombinedOutput()

	// 如果命令成功执行且输出包含测试字符串，说明SSH免密登录工作正常
	if err == nil && strings.Contains(string(output), "SSH_TEST_SUCCESS") {
		return true
	}

	return false
}

// DetectBestSSHMethod 检测最佳SSH设置方法
func DetectBestSSHMethod() SetupSSHMethod {
	// 检查expect是否可用
	if _, err := exec.LookPath("expect"); err == nil {
		return MethodExpect
	}

	// 默认使用ssh-copy-id方法
	return MethodSSHCopyID
}