package rainbond

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/rainbond/rainbond-offline-installer/pkg/config"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

type RainbondInstaller struct {
	config     *config.Config
	logger     *logrus.Logger
	chartPath  string
}

func NewRainbondInstaller(cfg *config.Config) *RainbondInstaller {
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)

	return &RainbondInstaller{
		config:    cfg,
		logger:    logger,
		chartPath: "./rainbond-chart", // 默认chart路径
	}
}

func (r *RainbondInstaller) SetChartPath(path string) {
	r.chartPath = path
}

func (r *RainbondInstaller) Run() error {
	r.logger.Info("开始安装Rainbond...")

	// 检查Kubernetes集群状态
	if err := r.checkKubernetesReady(); err != nil {
		return fmt.Errorf("Kubernetes集群未就绪: %w", err)
	}

	// 检查Helm是否可用
	if err := r.checkHelmAvailable(); err != nil {
		return fmt.Errorf("Helm不可用: %w", err)
	}

	// 检查现有Rainbond部署
	if exists, err := r.checkExistingDeployment(); err != nil {
		return fmt.Errorf("检查现有Rainbond部署失败: %w", err)
	} else if exists {
		r.logger.Info("检测到Rainbond已存在，跳过安装")
		return nil
	}

	// 创建命名空间
	if err := r.createNamespace(); err != nil {
		return fmt.Errorf("创建命名空间失败: %w", err)
	}

	// 生成values文件
	valuesFile, err := r.generateValuesFile()
	if err != nil {
		return fmt.Errorf("生成values文件失败: %w", err)
	}

	// 安装Rainbond Helm Chart
	if err := r.installHelmChart(valuesFile); err != nil {
		return fmt.Errorf("安装Rainbond失败: %w", err)
	}

	r.logger.Info("🎉 Rainbond Helm安装命令执行完成!")
	return nil
}

func (r *RainbondInstaller) checkKubernetesReady() error {
	r.logger.Info("检查Kubernetes集群状态...")

	cmd := r.buildSSHCommand(r.config.Hosts[0], "kubectl get nodes")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl命令执行失败: %w, 输出: %s", err, string(output))
	}

	if strings.Contains(string(output), "Ready") {
		r.logger.Info("Kubernetes集群已就绪")
		return nil
	}

	return fmt.Errorf("Kubernetes集群未就绪")
}

func (r *RainbondInstaller) checkHelmAvailable() error {
	r.logger.Info("检查Helm可用性...")

	// 检查当前目录是否有helm二进制
	helmPath := "./helm"
	if err := exec.Command("test", "-f", helmPath).Run(); err != nil {
		return fmt.Errorf("当前目录未找到helm二进制文件")
	}

	// 检查第一台节点是否有helm
	cmd := r.buildSSHCommand(r.config.Hosts[0], "which helm")
	if err := cmd.Run(); err != nil {
		r.logger.Info("第一台节点未找到helm，正在安装...")
		if err := r.installHelmBinary(); err != nil {
			return fmt.Errorf("安装helm二进制失败: %w", err)
		}
	} else {
		r.logger.Info("第一台节点已安装helm")
	}

	r.logger.Info("Helm可用")
	return nil
}

func (r *RainbondInstaller) installHelmBinary() error {
	r.logger.Info("复制helm二进制到第一台节点...")

	helmPath := "./helm"
	host := r.config.Hosts[0]
	
	r.logger.Infof("正在向节点 %s 安装helm...", host.IP)

	// 复制helm二进制到远程节点
	var scpCmd *exec.Cmd
	if host.Password != "" {
		if _, err := exec.LookPath("sshpass"); err == nil {
			scpCmd = exec.Command("sshpass", "-p", host.Password, "scp",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				helmPath,
				fmt.Sprintf("%s@%s:/tmp/helm", host.User, host.IP))
		} else {
			return fmt.Errorf("需要sshpass来支持密码认证的文件传输")
		}
	} else if host.SSHKey != "" {
		scpCmd = exec.Command("scp",
			"-i", host.SSHKey,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			helmPath,
			fmt.Sprintf("%s@%s:/tmp/helm", host.User, host.IP))
	} else {
		scpCmd = exec.Command("scp",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			helmPath,
			fmt.Sprintf("%s@%s:/tmp/helm", host.User, host.IP))
	}

	if output, err := scpCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("复制helm到节点 %s 失败: %w, 输出: %s", host.IP, err, string(output))
	}

	// 移动helm到/usr/local/bin并设置权限
	installCmd := r.buildSSHCommand(host, "sudo mv /tmp/helm /usr/local/bin/helm && sudo chmod +x /usr/local/bin/helm")
	if output, err := installCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("安装helm到节点 %s 失败: %w, 输出: %s", host.IP, err, string(output))
	}

	// 验证安装
	verifyCmd := r.buildSSHCommand(host, "helm version --short")
	if output, err := verifyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("验证helm安装失败，节点 %s: %w, 输出: %s", host.IP, err, string(output))
	}

	r.logger.Infof("节点 %s helm安装成功", host.IP)
	return nil
}

func (r *RainbondInstaller) checkExistingDeployment() (bool, error) {
	r.logger.Info("检查现有Rainbond部署...")

	namespace := r.config.Rainbond.Namespace
	if namespace == "" {
		namespace = "rbd-system"
	}

	cmd := r.buildSSHCommand(r.config.Hosts[0], fmt.Sprintf("kubectl get rainbondcluster -n %s", namespace))
	err := cmd.Run()
	return err == nil, nil
}

func (r *RainbondInstaller) createNamespace() error {
	namespace := r.config.Rainbond.Namespace
	if namespace == "" {
		namespace = "rbd-system"
	}

	r.logger.Infof("创建命名空间 %s...", namespace)

	// 检查命名空间是否已存在
	cmd := r.buildSSHCommand(r.config.Hosts[0], fmt.Sprintf("kubectl get namespace %s", namespace))
	if err := cmd.Run(); err == nil {
		r.logger.Infof("命名空间 %s 已存在，跳过创建", namespace)
		return nil
	}

	// 创建命名空间
	cmd = r.buildSSHCommand(r.config.Hosts[0], fmt.Sprintf("kubectl create namespace %s", namespace))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("创建命名空间失败: %w, 输出: %s", err, string(output))
	}

	r.logger.Infof("命名空间 %s 创建成功", namespace)
	return nil
}

func (r *RainbondInstaller) generateValuesFile() (string, error) {
	r.logger.Info("重新生成Helm values文件（基于最新配置）...")

	// 合并默认配置和用户配置
	values := make(map[string]interface{})

	// 设置基础配置
	namespace := r.config.Rainbond.Namespace
	if namespace == "" {
		namespace = "rbd-system"
	}

	// 如果用户有自定义values，使用用户的配置
	if r.config.Rainbond.Values != nil {
		values = r.config.Rainbond.Values
	}

	// 如果启用了MySQL，自动配置数据库连接
	if r.config.MySQL.Enabled {
		r.logger.Info("检测到MySQL已启用，自动配置数据库连接...")
		
		cluster, ok := values["Cluster"].(map[string]interface{})
		if !ok {
			cluster = make(map[string]interface{})
			values["Cluster"] = cluster
		}

		// 配置region数据库
		cluster["regionDatabase"] = map[string]interface{}{
			"enable":   true,
			"host":     "mysql-master.rbd-system.svc.cluster.local",
			"port":     3306,
			"name":     "region",
			"username": "root",
			"password": r.config.MySQL.RootPassword,
		}

		// 配置console数据库
		cluster["uiDatabase"] = map[string]interface{}{
			"enable":   true,
			"host":     "mysql-master.rbd-system.svc.cluster.local",
			"port":     3306,
			"name":     "console",
			"username": "root",
			"password": r.config.MySQL.RootPassword,
		}
	}

	// 转换为YAML，设置正确的缩进
	encoder := yaml.NewEncoder(nil)
	encoder.SetIndent(4) // 设置4个空格缩进
	
	var yamlBuffer strings.Builder
	encoder = yaml.NewEncoder(&yamlBuffer)
	encoder.SetIndent(4)
	
	err := encoder.Encode(values)
	if err != nil {
		return "", fmt.Errorf("序列化values失败: %w", err)
	}
	encoder.Close()
	
	yamlData := yamlBuffer.String()

	// 写入临时文件，每次重新生成确保使用最新配置
	valuesFile := "/tmp/rainbond-values.yaml"
	
	// 先删除旧的values文件
	cleanCmd := r.buildSSHCommand(r.config.Hosts[0], fmt.Sprintf("rm -f %s", valuesFile))
	cleanCmd.Run() // 忽略删除错误
	
	// 写入新的values文件
	writeCmd := fmt.Sprintf("cat > %s << 'EOF'\n%s\nEOF", valuesFile, yamlData)

	cmd := r.buildSSHCommand(r.config.Hosts[0], writeCmd)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("写入values文件失败: %w", err)
	}

	r.logger.Infof("Values文件已重新生成并保存至: %s", valuesFile)
	if len(yamlData) > 200 {
		r.logger.Debugf("Values内容预览: %s...", yamlData[:200])
	} else {
		r.logger.Debugf("Values内容: %s", yamlData)
	}
	return valuesFile, nil
}

func (r *RainbondInstaller) installHelmChart(valuesFile string) error {
	r.logger.Info("开始安装Rainbond Helm Chart...")

	namespace := r.config.Rainbond.Namespace
	if namespace == "" {
		namespace = "rbd-system"
	}

	// 构建helm install命令
	releaseName := "rainbond"

	// 先将chart tgz包传输到远程节点
	if err := r.transferChartToRemote(); err != nil {
		return fmt.Errorf("传输chart包到远程节点失败: %w", err)
	}

	remoteTgzPath := "/tmp/rainbond.tgz"
	helmCmd := fmt.Sprintf("helm install %s %s --namespace %s --values %s --create-namespace --wait --timeout=20m",
		releaseName, remoteTgzPath, namespace, valuesFile)

	r.logger.Infof("执行helm install: %s", helmCmd)
	cmd := r.buildSSHCommand(r.config.Hosts[0], helmCmd)
	
	// 设置较长的超时时间
	output, err := cmd.CombinedOutput()
	if err != nil {
		r.logger.Errorf("Helm安装输出: %s", string(output))
		return fmt.Errorf("helm install失败: %w", err)
	}

	r.logger.Info("Rainbond Helm Chart安装成功")
	r.logger.Infof("Helm安装输出: %s", string(output))
	return nil
}

func (r *RainbondInstaller) transferChartToRemote() error {
	r.logger.Info("传输Helm Chart包到远程节点...")

	host := r.config.Hosts[0]
	
	// 检查是否有现成的tgz包
	tgzPath := "./rainbond.tgz"
	if err := exec.Command("test", "-f", tgzPath).Run(); err != nil {
		return fmt.Errorf("未找到rainbond.tgz包文件")
	}

	// 传输tgz包到远程节点
	var scpCmd *exec.Cmd
	if host.Password != "" {
		if _, err := exec.LookPath("sshpass"); err == nil {
			scpCmd = exec.Command("sshpass", "-p", host.Password, "scp",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				tgzPath,
				fmt.Sprintf("%s@%s:/tmp/rainbond.tgz", host.User, host.IP))
		} else {
			return fmt.Errorf("需要sshpass来支持密码认证的文件传输")
		}
	} else if host.SSHKey != "" {
		scpCmd = exec.Command("scp",
			"-i", host.SSHKey,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			tgzPath,
			fmt.Sprintf("%s@%s:/tmp/rainbond.tgz", host.User, host.IP))
	} else {
		scpCmd = exec.Command("scp",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			tgzPath,
			fmt.Sprintf("%s@%s:/tmp/rainbond.tgz", host.User, host.IP))
	}

	if output, err := scpCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("传输tgz包失败: %w, 输出: %s", err, string(output))
	}

	r.logger.Info("Chart tgz包传输完成")
	return nil
}


func (r *RainbondInstaller) buildSSHCommand(host config.Host, command string) *exec.Cmd {
	var sshCmd *exec.Cmd

	if host.Password != "" {
		if _, err := exec.LookPath("sshpass"); err == nil {
			sshCmd = exec.Command("sshpass", "-p", host.Password, "ssh",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				fmt.Sprintf("%s@%s", host.User, host.IP),
				command)
		} else {
			sshCmd = exec.Command("ssh",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "BatchMode=no",
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