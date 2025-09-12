package rainbond

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/rainbond/rainbond-offline-installer/pkg/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"gopkg.in/yaml.v3"
)

// Logger 定义日志接口
type Logger interface {
	Debug(format string, v ...interface{})
	Info(format string, v ...interface{})
	Warn(format string, v ...interface{})
	Error(format string, v ...interface{})
}

// StepProgress 进度接口
type StepProgress interface {
	StartSubSteps(totalSubSteps int)
	StartSubStep(subStepName string)
	CompleteSubStep()
	CompleteSubSteps()
	StartNodeProcessing(nodeIP string)
	CompleteNodeStep(nodeIP string)
}

type RainbondInstaller struct {
	config         *config.Config
	logger         Logger
	stepProgress   StepProgress
	chartPath      string
	kubeConfig     *rest.Config
	kubeClient     kubernetes.Interface
	kubeConfigPath string
}

func NewRainbondInstaller(cfg *config.Config) *RainbondInstaller {
	return NewRainbondInstallerWithLogger(cfg, nil)
}

func NewRainbondInstallerWithLogger(cfg *config.Config, logger Logger) *RainbondInstaller {
	return NewRainbondInstallerWithLoggerAndProgress(cfg, logger, nil)
}

func NewRainbondInstallerWithLoggerAndProgress(cfg *config.Config, logger Logger, stepProgress StepProgress) *RainbondInstaller {
	r := &RainbondInstaller{
		config:       cfg,
		logger:       logger,
		stepProgress: stepProgress,
		chartPath:    "./rainbond.tgz", // 使用tgz包
	}
	// 初始化Kubernetes客户端和Helm配置
	if err := r.initializeClients(); err != nil {
		if logger != nil {
			logger.Error("初始化Kubernetes和Helm客户端失败: %v", err)
		}
	}
	return r
}

func (r *RainbondInstaller) SetChartPath(path string) {
	r.chartPath = path
}

// 初始化Kubernetes客户端
func (r *RainbondInstaller) initializeClients() error {
	// 获取kubeconfig
	kubeConfigPath, err := r.getKubeConfig()
	if err != nil {
		return fmt.Errorf("获取kubeconfig失败: %w", err)
	}
	r.kubeConfigPath = kubeConfigPath

	if r.logger != nil {
		r.logger.Debug("使用kubeconfig文件: %s", kubeConfigPath)
	}

	// 创建Kubernetes配置
	config, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	if err != nil {
		return fmt.Errorf("构建Kubernetes配置失败: %w", err)
	}
	r.kubeConfig = config

	if r.logger != nil {
		r.logger.Debug("Kubernetes API服务器地址: %s", config.Host)
	}

	// 创建Kubernetes客户端
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("创建Kubernetes客户端失败: %w", err)
	}
	r.kubeClient = clientset

	// 测试连接
	if r.logger != nil {
		r.logger.Debug("测试Kubernetes集群连接...")
	}
	nodes, err := clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("测试Kubernetes连接失败: %w", err)
	}
	if r.logger != nil {
		r.logger.Debug("成功连接到Kubernetes集群，发现 %d 个节点", len(nodes.Items))
	}

	return nil
}

// 获取kubeconfig文件路径
func (r *RainbondInstaller) getKubeConfig() (string, error) {
	// 优先使用RKE2模块保存的本地kubeconfig文件
	localKubeConfigPath := "./kubeconfig"

	// 检查本地kubeconfig是否存在
	if _, err := os.Stat(localKubeConfigPath); err == nil {
		if r.logger != nil {
			r.logger.Info("使用RKE2安装时保存的本地kubeconfig文件")
		}
		return localKubeConfigPath, nil
	}
	
	// 如果本地文件不存在，返回错误
	return "", fmt.Errorf("本地kubeconfig文件不存在: %s，请先运行RKE2安装", localKubeConfigPath)
}

// 更新kubeconfig中的server地址
func (r *RainbondInstaller) updateKubeConfigServer(kubeconfigPath, serverIP string) error {
	// 读取kubeconfig文件
	data, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		return err
	}

	// 替换server地址
	content := strings.ReplaceAll(string(data), "https://127.0.0.1:6443", fmt.Sprintf("https://%s:6443", serverIP))

	// 写回文件
	return os.WriteFile(kubeconfigPath, []byte(content), 0644)
}

// 执行命令的通用方法
func (r *RainbondInstaller) executeCommand(name string, args ...string) error {
	cmd := r.buildCommand(name, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("命令执行失败: %w, 输出: %s", err, string(output))
	}
	return nil
}

func (r *RainbondInstaller) Run() error {
	if r.logger != nil {
		r.logger.Info("开始安装Rainbond...")
	}

	// 确保客户端已初始化
	if r.kubeClient == nil {
		if r.logger != nil {
			r.logger.Debug("初始化Kubernetes和Helm客户端...")
		}
		if err := r.initializeClients(); err != nil {
			return fmt.Errorf("初始化客户端失败: %w", err)
		}
		if r.logger != nil {
			r.logger.Debug("客户端初始化成功")
		}
	}

	// 检查Kubernetes集群状态
	if err := r.checkKubernetesReady(); err != nil {
		return fmt.Errorf("Kubernetes集群未就绪: %w", err)
	}

	// 检查现有Rainbond部署
	if exists, err := r.checkExistingDeployment(); err != nil {
		return fmt.Errorf("检查现有Rainbond部署失败: %w", err)
	} else if exists {
		if r.logger != nil {
			r.logger.Info("检测到Rainbond已存在，跳过安装")
		}
		return nil
	}

	// 创建命名空间
	if err := r.createNamespace(); err != nil {
		return fmt.Errorf("创建命名空间失败: %w", err)
	}

	// 生成values配置
	values, err := r.generateValues()
	if err != nil {
		return fmt.Errorf("生成values配置失败: %w", err)
	}

	// 安装Rainbond Helm Chart
	if err := r.installHelmChart(values); err != nil {
		return fmt.Errorf("安装Rainbond失败: %w", err)
	}

	if r.logger != nil {
		r.logger.Info("🎉 Rainbond Helm安装完成!")
	}
	return nil
}

func (r *RainbondInstaller) checkKubernetesReady() error {
	if r.logger != nil {
		r.logger.Info("检查Kubernetes集群状态...")
	}

	// 使用Kubernetes客户端直接检查节点状态
	nodes, err := r.kubeClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("获取节点列表失败: %w", err)
	}

	readyNodes := 0
	for _, node := range nodes.Items {
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				readyNodes++
				break
			}
		}
	}

	if readyNodes == 0 {
		return fmt.Errorf("没有就绪的节点")
	}

	if r.logger != nil {
		r.logger.Info("Kubernetes集群已就绪，%d 个节点就绪", readyNodes)
	}
	return nil
}

func (r *RainbondInstaller) checkExistingDeployment() (bool, error) {
	if r.logger != nil {
		r.logger.Info("检查现有Rainbond部署...")
	}

	namespace := r.config.Rainbond.Namespace
	if namespace == "" {
		namespace = "rbd-system"
	}

	if r.logger != nil {
		r.logger.Debug("使用Helm CLI检查现有部署，目标命名空间: %s", namespace)
	}

	// 使用Helm CLI检查是否已安装
	cmd := r.buildHelmCommand("list", "-n", namespace, "-f", "rainbond", "-q")
	output, err := cmd.Output()
	if err != nil {
		if r.logger != nil {
			r.logger.Debug("Helm list命令执行失败: %v", err)
		}
		// 如果是因为命名空间不存在等原因，认为没有部署
		return false, nil
	}

	releases := strings.TrimSpace(string(output))
	if releases != "" {
		if r.logger != nil {
			r.logger.Info("发现现有Rainbond部署: %s", releases)
		}
		return true, nil
	}

	if r.logger != nil {
		r.logger.Debug("未发现现有Rainbond部署")
	}
	return false, nil
}

func (r *RainbondInstaller) createNamespace() error {
	namespace := r.config.Rainbond.Namespace
	if namespace == "" {
		namespace = "rbd-system"
	}

	if r.logger != nil {
		r.logger.Info("创建命名空间 %s...", namespace)
	}

	// 使用Kubernetes客户端检查命名空间是否已存在
	_, err := r.kubeClient.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err == nil {
		if r.logger != nil {
			r.logger.Info("命名空间 %s 已存在，跳过创建", namespace)
		}
		return nil
	}

	// 创建命名空间
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}

	_, err = r.kubeClient.CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("创建命名空间失败: %w", err)
	}

	if r.logger != nil {
		r.logger.Info("命名空间 %s 创建成功", namespace)
	}
	return nil
}

func (r *RainbondInstaller) generateValues() (map[string]interface{}, error) {
	if r.logger != nil {
		r.logger.Info("生成Helm values配置...")
	}

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
		if r.logger != nil {
			r.logger.Info("检测到MySQL已启用，自动配置数据库连接...")
		}

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

	if r.logger != nil {
		r.logger.Info("Values配置生成完成")
	}
	return values, nil
}

func (r *RainbondInstaller) installHelmChart(values map[string]interface{}) error {
	if r.logger != nil {
		r.logger.Info("开始安装Rainbond Helm Chart...")
	}

	namespace := r.config.Rainbond.Namespace
	if namespace == "" {
		namespace = "rbd-system"
	}

	releaseName := "rainbond"

	// 检查chart包是否存在
	if _, err := os.Stat(r.chartPath); os.IsNotExist(err) {
		return fmt.Errorf("chart包不存在: %s", r.chartPath)
	}

	if r.logger != nil {
		r.logger.Info("使用Helm CLI安装chart: %s 到命名空间: %s", releaseName, namespace)
	}

	// 构建Helm install命令 - 简化版本
	args := []string{
		"install", releaseName, r.chartPath,
		"--namespace", namespace,
		"--create-namespace", 
		"--timeout", "20m",
		"--wait",
	}

	// 如果有values配置，生成values文件并添加参数
	if len(values) > 0 {
		valuesFile, err := r.generateValuesFile(values)
		if err != nil {
			return fmt.Errorf("生成values文件失败: %w", err)
		}
		
		// 添加values参数
		args = append(args, "--values", valuesFile)
		
		if r.logger != nil {
			r.logger.Debug("使用values文件: %s", valuesFile)
		}
	}

	// 执行Helm install命令
	cmd := r.buildHelmCommand(args...)
	if r.logger != nil {
		r.logger.Debug("执行Helm命令: %s", strings.Join(append([]string{"helm"}, args...), " "))
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		if r.logger != nil {
			r.logger.Error("Helm安装失败: %v", err)
			r.logger.Error("Helm输出: %s", string(output))

			// 如果是名称冲突错误，尝试清理并重试
			if strings.Contains(string(output), "cannot re-use a name that is still in use") {
				r.logger.Info("检测到名称冲突，尝试清理现有release...")
				if cleanErr := r.cleanupExistingRelease(releaseName, namespace); cleanErr != nil {
					r.logger.Error("清理现有release失败: %v", cleanErr)
				} else {
					r.logger.Info("清理完成，重新尝试安装...")
					retryOutput, retryErr := cmd.CombinedOutput()
					if retryErr == nil {
						r.logger.Info("重新安装成功")
						r.logger.Info("Helm输出: %s", string(retryOutput))
						return nil
					} else {
						r.logger.Error("重新安装仍然失败: %v", retryErr)
						r.logger.Error("重试输出: %s", string(retryOutput))
					}
				}
			}
		}
		return fmt.Errorf("helm install失败: %w, 输出: %s", err, string(output))
	}

	if r.logger != nil {
		r.logger.Info("Rainbond Helm Chart安装成功")
		r.logger.Info("Helm输出: %s", string(output))
	}
	return nil
}

// cleanupExistingRelease 清理可能存在的Helm release残留
func (r *RainbondInstaller) cleanupExistingRelease(releaseName, namespace string) error {
	if r.logger != nil {
		r.logger.Debug("尝试清理release: %s (命名空间: %s)", releaseName, namespace)
	}

	// 使用Helm CLI删除
	cmd := r.buildHelmCommand("uninstall", releaseName, "-n", namespace)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if r.logger != nil {
			r.logger.Debug("Helm CLI删除失败: %v，输出: %s，尝试手动清理...", err, string(output))
		}

		// 如果Helm CLI失败，尝试手动清理Kubernetes资源
		return r.manualCleanupResources(releaseName, namespace)
	}

	if r.logger != nil {
		r.logger.Info("Helm release清理成功")
		r.logger.Debug("清理输出: %s", string(output))
	}
	return nil
}

// manualCleanupResources 手动清理Kubernetes资源
func (r *RainbondInstaller) manualCleanupResources(releaseName, namespace string) error {
	if r.logger != nil {
		r.logger.Debug("手动清理Kubernetes中的Helm相关资源...")
	}

	// 清理Helm存储的Secret
	labelSelector := fmt.Sprintf("owner=helm,name=%s", releaseName)

	secrets, err := r.kubeClient.CoreV1().Secrets(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return fmt.Errorf("查询Helm secrets失败: %w", err)
	}

	for _, secret := range secrets.Items {
		if r.logger != nil {
			r.logger.Debug("删除Helm secret: %s", secret.Name)
		}
		err := r.kubeClient.CoreV1().Secrets(namespace).Delete(context.Background(), secret.Name, metav1.DeleteOptions{})
		if err != nil {
			r.logger.Warn("删除secret %s 失败: %v", secret.Name, err)
		}
	}

	if r.logger != nil {
		r.logger.Info("手动清理完成，删除了 %d 个Helm相关的Secret", len(secrets.Items))
	}

	return nil
}

// buildHelmCommand 构建Helm命令
func (r *RainbondInstaller) buildHelmCommand(args ...string) *exec.Cmd {
	var helmPath string
	
	// 优先使用当前目录下的helm二进制文件
	if _, err := os.Stat("./helm"); err == nil {
		helmPath = "./helm"
		if r.logger != nil {
			r.logger.Debug("使用当前目录下的helm二进制: %s", helmPath)
		}
	} else {
		// 回退到系统PATH中的helm
		helmPath = "helm"
		if r.logger != nil {
			r.logger.Debug("使用系统PATH中的helm")
		}
	}
	
	cmd := exec.Command(helmPath, args...)
	// 设置KUBECONFIG环境变量
	if r.kubeConfigPath != "" {
		cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", r.kubeConfigPath))
	}
	return cmd
}

// generateValuesFile 生成values文件
func (r *RainbondInstaller) generateValuesFile(values map[string]interface{}) (string, error) {
	valuesFileName := "./rainbond-values.yaml"
	
	// 如果values为空，创建一个空文件
	if len(values) == 0 {
		file, err := os.Create(valuesFileName)
		if err != nil {
			return "", fmt.Errorf("创建values文件失败: %w", err)
		}
		file.Close()
		return valuesFileName, nil
	}

	// 使用标准YAML库转换map为YAML格式
	yamlContent, err := yaml.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("转换values为YAML失败: %w", err)
	}

	// 创建文件到当前目录
	file, err := os.Create(valuesFileName)
	if err != nil {
		return "", fmt.Errorf("创建values文件失败: %w", err)
	}

	// 写入YAML内容
	if _, err := file.Write(yamlContent); err != nil {
		file.Close()
		os.Remove(valuesFileName)
		return "", fmt.Errorf("写入values文件失败: %w", err)
	}

	if err := file.Close(); err != nil {
		os.Remove(valuesFileName)
		return "", fmt.Errorf("关闭values文件失败: %w", err)
	}

	if r.logger != nil {
		r.logger.Info("生成values文件: %s", valuesFileName)
		r.logger.Debug("Values内容:\n%s", yamlContent)
	}

	return valuesFileName, nil
}


// buildCommand 构建命令的通用方法
func (r *RainbondInstaller) buildCommand(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
