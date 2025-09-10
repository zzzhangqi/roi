package optimize

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/rainbond/rainbond-offline-installer/pkg/config"
)

// Logger 定义日志接口
type Logger interface {
	Debug(format string, v ...interface{})
	Info(format string, v ...interface{})
	Warn(format string, v ...interface{})
	Error(format string, v ...interface{})
}

type SystemOptimizer struct {
	config *config.Config
	logger Logger
}

func NewSystemOptimizer(cfg *config.Config) *SystemOptimizer {
	return NewSystemOptimizerWithLogger(cfg, nil)
}

func NewSystemOptimizerWithLogger(cfg *config.Config, logger Logger) *SystemOptimizer {
	return &SystemOptimizer{
		config: cfg,
		logger: logger,
	}
}

func (o *SystemOptimizer) Run() error {
	if o.logger != nil {
		o.logger.Info("开始系统优化...")
	}

	for i, host := range o.config.Hosts {
		if o.logger != nil {
			o.logger.Info("主机 %s: 开始系统优化...", host.IP)
		}

		// 检查是否为 root 用户
		if err := o.checkRootUser(host); err != nil {
			return fmt.Errorf("主机[%d] %s: %w", i, host.IP, err)
		}

		// 执行优化步骤
		if err := o.disableFirewalld(host); err != nil {
			if o.logger != nil {
				o.logger.Warn("主机 %s: 禁用防火墙服务失败: %v", host.IP, err)
			}
		}

		if err := o.disableUFW(host); err != nil {
			if o.logger != nil {
				o.logger.Warn("主机 %s: 禁用UFW防火墙失败: %v", host.IP, err)
			}
		}

		if err := o.disableSELinux(host); err != nil {
			if o.logger != nil {
				o.logger.Warn("主机 %s: 禁用SELinux失败: %v", host.IP, err)
			}
		}

		if err := o.disableSwap(host); err != nil {
			if o.logger != nil {
				o.logger.Warn("主机 %s: 禁用交换分区失败: %v", host.IP, err)
			}
		}

		if err := o.optimizeKernelParameters(host); err != nil {
			if o.logger != nil {
				o.logger.Warn("主机 %s: 优化内核参数失败: %v", host.IP, err)
			}
		}

		if err := o.optimizeSystemLimits(host); err != nil {
			if o.logger != nil {
				o.logger.Warn("主机 %s: 优化系统限制失败: %v", host.IP, err)
			}
		}

		if o.logger != nil {
			o.logger.Info("主机 %s: 系统优化完成", host.IP)
		}
	}

	if o.logger != nil {
		o.logger.Info("系统优化全部完成!")
	}
	return nil
}

func (o *SystemOptimizer) checkRootUser(host config.Host) error {
	sshCmd := o.buildSSHCommand(host, "test $(id -u) -eq 0")
	if err := sshCmd.Run(); err != nil {
		return fmt.Errorf("必须以root用户身份运行，请使用sudo")
	}
	return nil
}

func (o *SystemOptimizer) disableFirewalld(host config.Host) error {
	if o.logger != nil {
		o.logger.Info("主机 %s: 禁用firewalld防火墙...", host.IP)
	}

	// 检查 firewalld 是否安装
	sshCmd := o.buildSSHCommand(host, "command -v firewall-cmd")
	if err := sshCmd.Run(); err != nil {
		if o.logger != nil {
			o.logger.Info("主机 %s: 系统未安装firewalld", host.IP)
		}
		return nil
	}

	// 检查 firewalld 是否启用
	sshCmd = o.buildSSHCommand(host, "systemctl is-enabled firewalld")
	output, err := sshCmd.Output()
	isEnabled := err == nil && strings.TrimSpace(string(output)) == "enabled"

	// 检查 firewalld 是否运行
	sshCmd = o.buildSSHCommand(host, "systemctl is-active firewalld")
	output, err = sshCmd.Output()
	isActive := err == nil && strings.TrimSpace(string(output)) == "active"

	if !isActive && !isEnabled {
		if o.logger != nil {
			o.logger.Info("主机 %s: firewalld已禁用，跳过操作", host.IP)
		}
		return nil
	}

	if isActive {
		// 停止firewalld服务
		if o.logger != nil {
			o.logger.Info("主机 %s: 停止firewalld服务", host.IP)
		}
		sshCmd = o.buildSSHCommand(host, "systemctl stop firewalld")
		if err := sshCmd.Run(); err != nil {
			return fmt.Errorf("停止firewalld服务失败: %w", err)
		}
		if o.logger != nil {
			o.logger.Info("主机 %s: 成功停止firewalld服务", host.IP)
		}
	}

	if isEnabled {
		// 禁用firewalld服务
		if o.logger != nil {
			o.logger.Info("主机 %s: 禁用firewalld开机自启", host.IP)
		}
		sshCmd = o.buildSSHCommand(host, "systemctl disable firewalld")
		if err := sshCmd.Run(); err != nil {
			return fmt.Errorf("禁用firewalld开机自启失败: %w", err)
		}
		if o.logger != nil {
			o.logger.Info("主机 %s: 成功禁用firewalld开机自启", host.IP)
		}
	}

	return nil
}

func (o *SystemOptimizer) disableUFW(host config.Host) error {
	if o.logger != nil {
		o.logger.Info("主机 %s: 禁用UFW防火墙...", host.IP)
	}

	// 检查 UFW 是否安装
	sshCmd := o.buildSSHCommand(host, "command -v ufw")
	if err := sshCmd.Run(); err != nil {
		if o.logger != nil {
			o.logger.Info("主机 %s: 系统未安装UFW", host.IP)
		}
		return nil
	}

	// 检查 UFW 防火墙状态
	sshCmd = o.buildSSHCommand(host, "ufw status | head -1")
	output, err := sshCmd.Output()
	var firewallActive bool
	if err == nil {
		status := strings.TrimSpace(string(output))
		firewallActive = strings.Contains(status, "active")
	}

	// 检查 UFW 服务状态
	sshCmd = o.buildSSHCommand(host, "systemctl is-active ufw 2>/dev/null")
	output, err = sshCmd.Output()
	serviceActive := err == nil && strings.TrimSpace(string(output)) == "active"

	// 检查 UFW 服务是否启用
	sshCmd = o.buildSSHCommand(host, "systemctl is-enabled ufw 2>/dev/null")
	output, err = sshCmd.Output()
	serviceEnabled := err == nil && strings.TrimSpace(string(output)) == "enabled"

	// 如果防火墙和服务都已禁用，跳过操作
	if !firewallActive && !serviceActive && !serviceEnabled {
		if o.logger != nil {
			o.logger.Info("主机 %s: UFW已完全禁用，跳过操作", host.IP)
		}
		return nil
	}

	// 禁用 UFW 防火墙
	if firewallActive {
		if o.logger != nil {
			o.logger.Info("主机 %s: 禁用UFW防火墙规则", host.IP)
		}
		sshCmd = o.buildSSHCommand(host, "ufw --force disable")
		if err := sshCmd.Run(); err != nil {
			if o.logger != nil {
				o.logger.Warn("主机 %s: 禁用UFW防火墙规则失败: %v", host.IP, err)
			}
		} else {
			if o.logger != nil {
				o.logger.Info("主机 %s: 成功禁用UFW防火墙规则", host.IP)
			}
		}
	}

	// 停止 UFW 服务
	if serviceActive {
		if o.logger != nil {
			o.logger.Info("主机 %s: 停止UFW服务", host.IP)
		}
		sshCmd = o.buildSSHCommand(host, "systemctl stop ufw")
		if err := sshCmd.Run(); err != nil {
			if o.logger != nil {
				o.logger.Warn("主机 %s: 停止UFW服务失败: %v", host.IP, err)
			}
		} else {
			if o.logger != nil {
				o.logger.Info("主机 %s: 成功停止UFW服务", host.IP)
			}
		}
	}

	// 禁用 UFW 服务开机自启
	if serviceEnabled {
		if o.logger != nil {
			o.logger.Info("主机 %s: 禁用UFW开机自启", host.IP)
		}
		sshCmd = o.buildSSHCommand(host, "systemctl disable ufw")
		if err := sshCmd.Run(); err != nil {
			if o.logger != nil {
				o.logger.Warn("主机 %s: 禁用UFW开机自启失败: %v", host.IP, err)
			}
		} else {
			if o.logger != nil {
				o.logger.Info("主机 %s: 成功禁用UFW开机自启", host.IP)
			}
		}
	}

	return nil
}

func (o *SystemOptimizer) disableSELinux(host config.Host) error {
	if o.logger != nil {
		o.logger.Info("主机 %s: 禁用SELinux...", host.IP)
	}

	// 检查 SELinux 是否安装
	sshCmd := o.buildSSHCommand(host, "command -v getenforce")
	if err := sshCmd.Run(); err != nil {
		if o.logger != nil {
			o.logger.Info("主机 %s: 系统未安装SELinux", host.IP)
		}
		return nil
	}

	// 获取当前 SELinux 状态
	sshCmd = o.buildSSHCommand(host, "getenforce")
	output, err := sshCmd.Output()
	if err != nil {
		return fmt.Errorf("获取SELinux状态失败: %w", err)
	}

	currentStatus := strings.TrimSpace(string(output))
	
	// 检查配置文件中的SELinux设置
	sshCmd = o.buildSSHCommand(host, "grep '^SELINUX=' /etc/selinux/config 2>/dev/null | cut -d= -f2")
	configOutput, _ := sshCmd.Output()
	configStatus := strings.TrimSpace(string(configOutput))

	// 如果已经完全禁用，跳过操作
	if currentStatus == "Disabled" && configStatus == "disabled" {
		if o.logger != nil {
			o.logger.Info("主机 %s: SELinux已完全禁用，跳过操作", host.IP)
		}
		return nil
	}

	if o.logger != nil {
		o.logger.Info("主机 %s: 当前SELinux状态: %s，配置文件状态: %s", host.IP, currentStatus, configStatus)
	}

	// 临时禁用 SELinux（如果当前是启用状态）
	if currentStatus == "Enforcing" || currentStatus == "Permissive" {
		if o.logger != nil {
			o.logger.Info("主机 %s: 临时禁用SELinux", host.IP)
		}
		sshCmd = o.buildSSHCommand(host, "setenforce 0")
		if err := sshCmd.Run(); err != nil {
			if o.logger != nil {
				o.logger.Warn("主机 %s: 临时禁用SELinux失败: %v", host.IP, err)
			}
		} else {
			if o.logger != nil {
				o.logger.Info("主机 %s: 成功临时禁用SELinux", host.IP)
			}
		}
	}

	// 永久禁用 SELinux（修改配置文件）
	if configStatus != "disabled" {
		if o.logger != nil {
			o.logger.Info("主机 %s: 永久禁用SELinux（修改配置文件）", host.IP)
		}
		sshCmd = o.buildSSHCommand(host, "sed -i 's/^SELINUX=.*/SELINUX=disabled/' /etc/selinux/config")
		if err := sshCmd.Run(); err != nil {
			return fmt.Errorf("永久禁用SELinux失败: %w", err)
		}
		if o.logger != nil {
			o.logger.Info("主机 %s: 成功永久禁用SELinux（需要重启生效）", host.IP)
		}
	}

	return nil
}

func (o *SystemOptimizer) disableSwap(host config.Host) error {
	if o.logger != nil {
		o.logger.Info("主机 %s: 禁用交换分区...", host.IP)
	}

	// 检查是否有激活的交换分区
	sshCmd := o.buildSSHCommand(host, "swapon -s | wc -l")
	output, err := sshCmd.Output()
	var swapActive bool
	if err == nil {
		lines := strings.TrimSpace(string(output))
		swapActive = lines != "0" && lines != "1" // 标题行算1行，所以>1表示有swap
	}

	// 检查 /etc/fstab 中是否有swap条目
	sshCmd = o.buildSSHCommand(host, "grep -v '^#' /etc/fstab | grep -c swap || true")
	output, err = sshCmd.Output()
	var swapInFstab bool
	if err == nil {
		count := strings.TrimSpace(string(output))
		swapInFstab = count != "0"
	}

	// 如果交换分区已完全禁用，跳过操作
	if !swapActive && !swapInFstab {
		if o.logger != nil {
			o.logger.Info("主机 %s: 交换分区已完全禁用，跳过操作", host.IP)
		}
		return nil
	}

	// 禁用当前激活的交换分区
	if swapActive {
		if o.logger != nil {
			o.logger.Info("主机 %s: 关闭当前激活的交换分区", host.IP)
		}
		sshCmd = o.buildSSHCommand(host, "swapoff -a")
		if err := sshCmd.Run(); err != nil {
			if o.logger != nil {
				o.logger.Warn("主机 %s: 关闭交换分区失败: %v", host.IP, err)
			}
		} else {
			if o.logger != nil {
				o.logger.Info("主机 %s: 成功关闭激活的交换分区", host.IP)
			}
		}
	}

	// 注释掉 /etc/fstab 中的交换分区条目
	if swapInFstab {
		if o.logger != nil {
			o.logger.Info("主机 %s: 注释/etc/fstab中的交换分区条目", host.IP)
		}
		sshCmd = o.buildSSHCommand(host, "sed -i '/swap/s/^/#/' /etc/fstab")
		if err := sshCmd.Run(); err != nil {
			return fmt.Errorf("注释/etc/fstab中的交换分区条目失败: %w", err)
		}
		if o.logger != nil {
			o.logger.Info("主机 %s: 成功注释/etc/fstab中的交换分区条目", host.IP)
		}
	}

	return nil
}


func (o *SystemOptimizer) optimizeKernelParameters(host config.Host) error {
	if o.logger != nil {
		o.logger.Info("主机 %s: 优化内核参数...", host.IP)
	}

	// 检查是否已经优化过（通过检查配置文件是否存在我们的标识）
	sshCmd := o.buildSSHCommand(host, "grep -q 'Network bridge settings for container networking' /etc/sysctl.conf 2>/dev/null")
	if err := sshCmd.Run(); err == nil {
		if o.logger != nil {
			o.logger.Info("主机 %s: 内核参数已优化，跳过操作", host.IP)
		}
		return nil
	}

	// 创建优化的 sysctl 配置（兼容性更好的版本）
	sysctlConfig := `# Network bridge settings for container networking
net.bridge.bridge-nf-call-ip6tables=1
net.bridge.bridge-nf-call-iptables=1
net.ipv4.ip_forward=1
net.ipv4.conf.all.forwarding=1

# Neighbor table settings
net.ipv4.neigh.default.gc_thresh1=4096
net.ipv4.neigh.default.gc_thresh2=6144
net.ipv4.neigh.default.gc_thresh3=8192

# Performance monitoring
kernel.perf_event_paranoid=-1

# Sysctls for k8s node configuration
net.core.rmem_max=16777216
fs.inotify.max_user_watches=524288

# File system limits
fs.file-max=2097152
fs.inotify.max_user_instances=8192
fs.inotify.max_queued_events=16384
vm.max_map_count=262144

# Network performance tuning
net.core.netdev_max_backlog=16384
net.core.wmem_max=16777216
net.core.somaxconn=32768
net.ipv4.tcp_max_syn_backlog=8096

# Disable IPv6 (if not needed)
net.ipv6.conf.all.disable_ipv6=1
net.ipv6.conf.default.disable_ipv6=1
net.ipv6.conf.lo.disable_ipv6=1

# Memory and debugging settings
vm.swappiness=0

# Security settings
net.ipv4.conf.default.accept_source_route=0
net.ipv4.conf.all.accept_source_route=0
net.ipv4.conf.default.promote_secondaries=1
net.ipv4.conf.all.promote_secondaries=1

# Source route verification
net.ipv4.conf.all.rp_filter=0
net.ipv4.conf.default.rp_filter=0
net.ipv4.conf.default.arp_announce=2
net.ipv4.conf.lo.arp_announce=2
net.ipv4.conf.all.arp_announce=2

# TCP optimization
net.ipv4.tcp_max_tw_buckets=5000
net.ipv4.tcp_syncookies=1
net.ipv4.tcp_fin_timeout=30
net.ipv4.tcp_synack_retries=2`

	// 写入 sysctl 配置
	if o.logger != nil {
		o.logger.Info("主机 %s: 写入内核参数配置文件", host.IP)
	}
	sshCmd = o.buildSSHCommand(host, fmt.Sprintf("cat > /etc/sysctl.conf << 'EOF'\n%s\nEOF", sysctlConfig))
	if err := sshCmd.Run(); err != nil {
		return fmt.Errorf("写入内核参数配置失败: %w", err)
	}

	// 应用 sysctl 设置（忽略错误，因为某些参数可能不支持）
	if o.logger != nil {
		o.logger.Info("主机 %s: 应用内核参数设置", host.IP)
	}
	sshCmd = o.buildSSHCommand(host, "sysctl -p")
	if err := sshCmd.Run(); err != nil {
		if o.logger != nil {
			o.logger.Warn("主机 %s: 某些内核参数可能不被支持: %v", host.IP, err)
		}
		// 尝试逐个应用参数，忽略不支持的参数
		sshCmd = o.buildSSHCommand(host, "sysctl -p 2>&1 | grep -v 'No such file or directory' | grep -v 'Invalid argument' || true")
		sshCmd.Run() // 忽略错误
	}

	if o.logger != nil {
		o.logger.Info("主机 %s: 内核参数优化成功", host.IP)
	}
	return nil
}

func (o *SystemOptimizer) optimizeSystemLimits(host config.Host) error {
	if o.logger != nil {
		o.logger.Info("主机 %s: 优化系统限制...", host.IP)
	}

	// 检查是否已经优化过（通过检查配置文件是否包含我们的标识）
	sshCmd := o.buildSSHCommand(host, "grep -q 'Increased file descriptor limits for containerized workloads' /etc/security/limits.conf 2>/dev/null")
	if err := sshCmd.Run(); err == nil {
		if o.logger != nil {
			o.logger.Info("主机 %s: 系统限制已优化，跳过操作", host.IP)
		}
		return nil
	}

	// 创建优化的 limits 配置
	limitsConfig := `# Increased file descriptor limits for containerized workloads
* soft nofile 1024000
* hard nofile 1024000
* soft nproc 1024000
* hard nproc 1024000`

	// 写入 limits 配置
	if o.logger != nil {
		o.logger.Info("主机 %s: 写入系统限制配置文件", host.IP)
	}
	sshCmd = o.buildSSHCommand(host, fmt.Sprintf("cat > /etc/security/limits.conf << 'EOF'\n%s\nEOF", limitsConfig))
	if err := sshCmd.Run(); err != nil {
		return fmt.Errorf("写入系统限制配置失败: %w", err)
	}

	if o.logger != nil {
		o.logger.Info("主机 %s: 系统限制优化成功", host.IP)
	}
	return nil
}

func (o *SystemOptimizer) buildSSHCommand(host config.Host, command string) *exec.Cmd {
	var sshCmd *exec.Cmd

	if host.Password != "" {
		// 检查 sshpass 是否可用
		if _, err := exec.LookPath("sshpass"); err != nil {
			if o.logger != nil {
				o.logger.Warn("未找到sshpass工具，请安装sshpass或为主机 %s 使用SSH密钥认证", host.IP)
			}
			sshCmd = exec.Command("ssh",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "BatchMode=no",
				fmt.Sprintf("%s@%s", host.User, host.IP),
				command)
		} else {
			sshCmd = exec.Command("sshpass", "-p", host.Password, "ssh",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				fmt.Sprintf("%s@%s", host.User, host.IP),
				command)
		}
	} else if host.SSHKey != "" {
		sshCmd = exec.Command("ssh",
			"-i", host.SSHKey,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			fmt.Sprintf("%s@%s", host.User, host.IP),
			command)
	} else {
		sshCmd = exec.Command("ssh",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			fmt.Sprintf("%s@%s", host.User, host.IP),
			command)
	}

	return sshCmd
}
