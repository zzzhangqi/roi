package mysql

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rainbond/rainbond-offline-installer/pkg/config"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const mysqlMasterYAML = `---
# MySQL Master Service
apiVersion: v1
kind: Service
metadata:
  name: mysql-master
  namespace: rbd-system
  labels:
    app: mysql-master
spec:
  type: ClusterIP
  ports:
    - port: 3306
      targetPort: 3306
      protocol: TCP
  selector:
    app: mysql-master

---
# MySQL Master StatefulSet
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: mysql-master
  namespace: rbd-system
  labels:
    app: mysql-master
spec:
  serviceName: mysql-master
  replicas: 1
  selector:
    matchLabels:
      app: mysql-master
  template:
    metadata:
      labels:
        app: mysql-master
    spec:
      nodeName: "%s"
      containers:
      - name: mysql
        image: registry.cn-hangzhou.aliyuncs.com/goodrain/mysql:8.0.34-bitnami
        ports:
        - containerPort: 3306
        env:
        - name: MYSQL_ROOT_PASSWORD
          value: "%s"
        - name: MYSQL_REPLICATION_MODE
          value: "master"
        - name: MYSQL_REPLICATION_USER
          value: "%s"
        - name: MYSQL_REPLICATION_PASSWORD
          value: "%s"
        - name: MYSQL_AUTHENTICATION_PLUGIN
          value: "mysql_native_password"
        volumeMounts:
        - name: mysql-data
          mountPath: /bitnami/mysql/data
        resources:
          requests:
            memory: "1Gi"
            cpu: "500m"
          limits:
            memory: "2Gi"
            cpu: "1000m"
        livenessProbe:
          exec:
            command:
            - mysqladmin
            - ping
            - -h
            - localhost
          initialDelaySeconds: 30
          periodSeconds: 10
          timeoutSeconds: 5
        readinessProbe:
          exec:
            command:
            - mysqladmin
            - ping
            - -h
            - localhost
          initialDelaySeconds: 5
          periodSeconds: 5
          timeoutSeconds: 1
      volumes:
      - name: mysql-data
        hostPath:
          path: %s/master
          type: DirectoryOrCreate
`

const mysqlSlaveYAML = `---
# MySQL Slave Service
apiVersion: v1
kind: Service
metadata:
  name: mysql-slave
  namespace: rbd-system
  labels:
    app: mysql-slave
spec:
  type: ClusterIP
  ports:
    - port: 3306
      targetPort: 3306
      protocol: TCP
  selector:
    app: mysql-slave

---
# MySQL Slave StatefulSet
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: mysql-slave
  namespace: rbd-system
  labels:
    app: mysql-slave
spec:
  serviceName: mysql-slave
  replicas: 1
  selector:
    matchLabels:
      app: mysql-slave
  template:
    metadata:
      labels:
        app: mysql-slave
    spec:
      nodeName: "%s"
      containers:
      - name: mysql
        image: registry.cn-hangzhou.aliyuncs.com/goodrain/mysql:8.0.34-bitnami
        ports:
        - containerPort: 3306
        env:
        - name: MYSQL_MASTER_HOST
          value: "mysql-master-0.mysql-master.rbd-system.svc.cluster.local"
        - name: MYSQL_MASTER_ROOT_PASSWORD
          value: "%s"
        - name: MYSQL_MASTER_PORT_NUMBER
          value: "3306"
        - name: MYSQL_REPLICATION_MODE
          value: "slave"
        - name: MYSQL_REPLICATION_USER
          value: "%s"
        - name: MYSQL_REPLICATION_PASSWORD
          value: "%s"
        - name: MYSQL_AUTHENTICATION_PLUGIN
          value: "mysql_native_password"
        volumeMounts:
        - name: mysql-data
          mountPath: /bitnami/mysql/data
        resources:
          requests:
            memory: "1Gi"
            cpu: "500m"
          limits:
            memory: "2Gi"
            cpu: "1000m"
        livenessProbe:
          exec:
            command:
            - mysqladmin
            - ping
            - -h
            - localhost
          initialDelaySeconds: 60
          periodSeconds: 10
          timeoutSeconds: 5
        readinessProbe:
          exec:
            command:
            - mysqladmin
            - ping
            - -h
            - localhost
          initialDelaySeconds: 30
          periodSeconds: 5
          timeoutSeconds: 1
      volumes:
      - name: mysql-data
        hostPath:
          path: %s/slave
          type: DirectoryOrCreate
`

const mysqlInitYAML = `---
# MySQL Database Initialization Job
apiVersion: batch/v1
kind: Job
metadata:
  name: mysql-init-databases
  namespace: rbd-system
  labels:
    app: mysql-init
spec:
  ttlSecondsAfterFinished: 60
  template:
    metadata:
      labels:
        app: mysql-init
    spec:
      restartPolicy: OnFailure
      containers:
      - name: mysql-init
        image: registry.cn-hangzhou.aliyuncs.com/goodrain/mysql:8.0.34-bitnami
        command:
        - /bin/bash
        - -c
        - |
          echo "等待MySQL Master就绪..."
          until mysql -h mysql-master-0.mysql-master.rbd-system.svc.cluster.local -u root -p%s -e "SELECT 1" >/dev/null 2>&1; do
            echo "等待MySQL Master启动... ($(date))"
            sleep 5
          done
          
          echo "MySQL Master已就绪，开始创建数据库..."
          
          # 创建console数据库
          mysql -h mysql-master-0.mysql-master.rbd-system.svc.cluster.local -u root -p%s -e "CREATE DATABASE IF NOT EXISTS console CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
          if [ $? -eq 0 ]; then
            echo "console数据库创建成功"
          else
            echo "console数据库创建失败"
            exit 1
          fi
          
          # 创建region数据库  
          mysql -h mysql-master-0.mysql-master.rbd-system.svc.cluster.local -u root -p%s -e "CREATE DATABASE IF NOT EXISTS region CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
          if [ $? -eq 0 ]; then
            echo "region数据库创建成功"
          else
            echo "region数据库创建失败"
            exit 1
          fi
          
          
          echo "数据库初始化完成"
          
          # 验证主从同步状态
          echo "验证主从同步状态..."
          sleep 10
          
          echo "=== Master状态 ==="
          mysql -h mysql-master-0.mysql-master.rbd-system.svc.cluster.local -u root -p%s -e "SHOW MASTER STATUS\G"
          
          echo "=== 显示所有数据库 ==="
          mysql -h mysql-master-0.mysql-master.rbd-system.svc.cluster.local -u root -p%s -e "SHOW DATABASES;"
          
          # 检查是否有Slave节点并验证主从同步
          echo "检查Slave节点可用性..."
          
          # 尝试连接Slave节点来检测是否存在
          SLAVE_CONNECTED=false
          for i in {1..6}; do
            if mysql -h mysql-slave-0.mysql-slave.rbd-system.svc.cluster.local -u root -p%s -e "SELECT 1" >/dev/null 2>&1; then
              echo "检测到Slave节点，开始验证数据同步..."
              SLAVE_CONNECTED=true
              break
            fi
            echo "尝试连接Slave节点... ($i/6)"
            sleep 5
          done
          
          if [ "$SLAVE_CONNECTED" = "true" ]; then
            # 在Master上创建测试表来验证同步
            echo "=== 验证数据同步 ==="
            mysql -h mysql-master-0.mysql-master.rbd-system.svc.cluster.local -u root -p%s -e "
              USE console;
              CREATE TABLE IF NOT EXISTS sync_test (id INT PRIMARY KEY, test_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP);
              INSERT INTO sync_test (id) VALUES (1) ON DUPLICATE KEY UPDATE test_time = CURRENT_TIMESTAMP;
            "
            
            # 等待同步传播
            sleep 3
            
            # 验证数据同步
            if mysql -h mysql-slave-0.mysql-slave.rbd-system.svc.cluster.local -u root -p%s -e "SELECT * FROM console.sync_test WHERE id=1" >/dev/null 2>&1; then
              echo "✓ 数据同步验证成功: 测试数据已同步到Slave"
            else
              echo "✗ 警告: 数据同步验证失败"
            fi
            
            # 清理测试表
            mysql -h mysql-master-0.mysql-master.rbd-system.svc.cluster.local -u root -p%s -e "DROP TABLE IF EXISTS console.sync_test" >/dev/null 2>&1
          else
            echo "未检测到Slave节点或Slave节点未就绪，跳过主从同步验证"
          fi
          
          echo "MySQL集群初始化和验证完成!"
`

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

type MySQLInstaller struct {
	config       *config.Config
	logger       Logger
	stepProgress StepProgress
	kubeConfig   *rest.Config
	kubeClient   kubernetes.Interface
}

func NewMySQLInstaller(cfg *config.Config) *MySQLInstaller {
	return NewMySQLInstallerWithLogger(cfg, nil)
}

func NewMySQLInstallerWithLogger(cfg *config.Config, logger Logger) *MySQLInstaller {
	return NewMySQLInstallerWithLoggerAndProgress(cfg, logger, nil)
}

func NewMySQLInstallerWithLoggerAndProgress(cfg *config.Config, logger Logger, stepProgress StepProgress) *MySQLInstaller {
	m := &MySQLInstaller{
		config:       cfg,
		logger:       logger,
		stepProgress: stepProgress,
	}
	// 初始化Kubernetes客户端
	if err := m.initializeKubeClient(); err != nil {
		if logger != nil {
			logger.Error("初始化Kubernetes客户端失败: %v", err)
		}
	}
	return m
}

// 初始化Kubernetes客户端
func (m *MySQLInstaller) initializeKubeClient() error {
	// 优先使用本地kubeconfig文件
	localKubeConfigPath := "./kubeconfig"

	// 检查本地kubeconfig是否存在
	if _, err := os.Stat(localKubeConfigPath); err != nil {
		return fmt.Errorf("本地kubeconfig文件不存在: %s，请先运行RKE2安装", localKubeConfigPath)
	}

	// 创建Kubernetes配置
	config, err := clientcmd.BuildConfigFromFlags("", localKubeConfigPath)
	if err != nil {
		return fmt.Errorf("构建Kubernetes配置失败: %w", err)
	}
	m.kubeConfig = config

	// 创建Kubernetes客户端
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("创建Kubernetes客户端失败: %w", err)
	}
	m.kubeClient = clientset

	return nil
}

func (m *MySQLInstaller) Run() error {
	if m.logger != nil {
		m.logger.Info("开始部署MySQL主从集群...")
	}

	// 检查MySQL配置
	if !m.config.MySQL.Enabled {
		if m.logger != nil {
			m.logger.Info("MySQL部署未启用，跳过MySQL安装")
		}
		return nil
	}

	// 设置默认值
	m.setDefaults()

	// 验证RKE2集群是否就绪
	if err := m.checkKubernetesReady(); err != nil {
		return fmt.Errorf("Kubernetes集群未就绪: %w", err)
	}

	// 检查现有MySQL部署
	if exists, err := m.checkExistingDeployment(); err != nil {
		return fmt.Errorf("检查现有MySQL部署失败: %w", err)
	} else if exists {
		if m.logger != nil {
			m.logger.Info("检测到MySQL集群已存在，跳过部署")
		}
		return m.verifyDeployment()
	}

	// 创建数据存储目录
	if err := m.createDataDirectories(); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}

	// 创建命名空间
	if err := m.createNamespace(); err != nil {
		return fmt.Errorf("创建命名空间失败: %w", err)
	}

	// 部署MySQL Master
	if m.logger != nil {
		m.logger.Info("=== 部署MySQL Master ===")
	}
	if err := m.deployMaster(); err != nil {
		return fmt.Errorf("部署MySQL Master失败: %w", err)
	}

	// 检查是否有配置Slave节点，如果有则部署MySQL Slave
	if m.hasSlaveNode() {
		if m.logger != nil {
			m.logger.Info("=== 部署MySQL Slave ===")
		}
		if err := m.deploySlave(); err != nil {
			return fmt.Errorf("部署MySQL Slave失败: %w", err)
		}
	}

	// 等待部署就绪
	if err := m.waitForDeployment(); err != nil {
		return fmt.Errorf("等待MySQL集群就绪失败: %w", err)
	}

	// 初始化数据库
	if err := m.initializeDatabases(); err != nil {
		return fmt.Errorf("初始化数据库失败: %w", err)
	}

	// 验证部署
	if err := m.verifyDeployment(); err != nil {
		return fmt.Errorf("验证MySQL部署失败: %w", err)
	}

	if m.logger != nil {
		m.logger.Info("🎉 MySQL主从集群部署完成!")
	}
	return nil
}

func (m *MySQLInstaller) setDefaults() {
	if m.config.MySQL.RootPassword == "" {
		m.config.MySQL.RootPassword = "Root123456"
	}
	if m.config.MySQL.ReplUser == "" {
		m.config.MySQL.ReplUser = "repl_user"
	}
	if m.config.MySQL.ReplPassword == "" {
		m.config.MySQL.ReplPassword = "repl_password"
	}
	if m.config.MySQL.DataPath == "" {
		m.config.MySQL.DataPath = "/opt/rainbond/mysql"
	}
}

func (m *MySQLInstaller) checkKubernetesReady() error {
	if m.logger != nil {
		m.logger.Info("检查Kubernetes集群状态...")
	}

	// 确保Kubernetes客户端已初始化
	if m.kubeClient == nil {
		if err := m.initializeKubeClient(); err != nil {
			return fmt.Errorf("初始化Kubernetes客户端失败: %w", err)
		}
	}

	// 使用Kubernetes API获取节点状态
	nodes, err := m.kubeClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
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

	if m.logger != nil {
		m.logger.Info("Kubernetes集群已就绪，%d 个节点就绪", readyNodes)
	}
	return nil
}

func (m *MySQLInstaller) checkExistingDeployment() (bool, error) {
	if m.logger != nil {
		m.logger.Info("检查现有MySQL部署...")
	}

	// 确保Kubernetes客户端已初始化
	if m.kubeClient == nil {
		if err := m.initializeKubeClient(); err != nil {
			return false, fmt.Errorf("初始化Kubernetes客户端失败: %w", err)
		}
	}

	// 使用Kubernetes API检查StatefulSet是否存在
	_, err := m.kubeClient.AppsV1().StatefulSets("rbd-system").Get(context.TODO(), "mysql-master", metav1.GetOptions{})
	return err == nil, nil
}

func (m *MySQLInstaller) createDataDirectories() error {
	if m.logger != nil {
		m.logger.Info("创建MySQL数据存储目录...")
	}

	// 在MySQL Master节点上创建master数据目录
	masterHost := m.getMasterHost()
	if masterHost != nil {
		masterPath := fmt.Sprintf("%s/master", m.config.MySQL.DataPath)
		// 清理可能存在的目录，创建新目录，设置权限为1001:1001 (bitnami mysql user)
		cmd := m.buildSSHCommand(*masterHost, fmt.Sprintf(
			"rm -rf %s && mkdir -p %s && chown -R 1001:1001 %s && chmod -R 755 %s",
			masterPath, masterPath, masterPath, masterPath))

		if err := cmd.Run(); err != nil {
			if m.logger != nil {
				m.logger.Warn("主机 %s: 创建Master数据目录失败: %v", masterHost.IP, err)
			}
		} else {
			if m.logger != nil {
				m.logger.Info("主机 %s: Master数据目录创建成功", masterHost.IP)
			}
		}
	}

	// 在MySQL Slave节点上创建slave数据目录
	slaveHost := m.getSlaveHost()
	if slaveHost != nil {
		slavePath := fmt.Sprintf("%s/slave", m.config.MySQL.DataPath)
		// 清理可能存在的目录，创建新目录，设置权限为1001:1001 (bitnami mysql user)
		cmd := m.buildSSHCommand(*slaveHost, fmt.Sprintf(
			"rm -rf %s && mkdir -p %s && chown -R 1001:1001 %s && chmod -R 755 %s",
			slavePath, slavePath, slavePath, slavePath))

		if err := cmd.Run(); err != nil {
			if m.logger != nil {
				m.logger.Warn("主机 %s: 创建Slave数据目录失败: %v", slaveHost.IP, err)
			}
		} else {
			if m.logger != nil {
				m.logger.Info("主机 %s: Slave数据目录创建成功", slaveHost.IP)
			}
		}
	}

	return nil
}

func (m *MySQLInstaller) deployMaster() error {
	masterHost := m.getMasterHost()
	if masterHost == nil {
		return fmt.Errorf("没有找到配置为MySQL Master的节点")
	}

	masterNodeName := masterHost.NodeName
	if masterNodeName == "" {
		masterNodeName = masterHost.IP
	}

	// 生成MySQL Master YAML
	yamlContent := fmt.Sprintf(mysqlMasterYAML,
		masterNodeName,              // nodeName for direct binding
		m.config.MySQL.RootPassword, // MYSQL_ROOT_PASSWORD
		m.config.MySQL.ReplUser,     // MYSQL_REPLICATION_USER
		m.config.MySQL.ReplPassword, // MYSQL_REPLICATION_PASSWORD
		m.config.MySQL.DataPath,     // hostPath
	)

	// 使用Kubernetes API创建资源
	return m.applyYAMLOnFirstNode(yamlContent, "MySQL Master")
}

func (m *MySQLInstaller) deploySlave() error {
	slaveHost := m.getSlaveHost()
	if slaveHost == nil {
		return fmt.Errorf("没有找到配置为MySQL Slave的节点")
	}

	slaveNodeName := slaveHost.NodeName
	if slaveNodeName == "" {
		slaveNodeName = slaveHost.IP
	}

	// 生成MySQL Slave YAML
	yamlContent := fmt.Sprintf(mysqlSlaveYAML,
		slaveNodeName,               // nodeName for direct binding
		m.config.MySQL.RootPassword, // MYSQL_MASTER_ROOT_PASSWORD
		m.config.MySQL.ReplUser,     // MYSQL_REPLICATION_USER
		m.config.MySQL.ReplPassword, // MYSQL_REPLICATION_PASSWORD
		m.config.MySQL.DataPath,     // hostPath
	)

	// 使用Kubernetes API创建资源
	return m.applyYAMLOnFirstNode(yamlContent, "MySQL Slave")
}

func (m *MySQLInstaller) waitForDeployment() error {
	if m.logger != nil {
		m.logger.Info("等待MySQL部署就绪...")
	}

	// 等待MySQL Master就绪
	if m.logger != nil {
		m.logger.Info("等待MySQL Master就绪...")
	}
	if err := m.waitForPodsReady("app=mysql-master", "MySQL Master"); err != nil {
		return err
	}

	// 如果有Slave节点，等待Slave就绪
	if m.hasSlaveNode() {
		if m.logger != nil {
			m.logger.Info("等待MySQL Slave就绪...")
		}
		if err := m.waitForPodsReady("app=mysql-slave", "MySQL Slave"); err != nil {
			return err
		}
	}

	return nil
}

func (m *MySQLInstaller) initializeDatabases() error {
	if m.logger != nil {
		m.logger.Info("初始化MySQL数据库...")
	}

	// Job配置了ttlSecondsAfterFinished: 60，完成后会自动清理
	jobName := "mysql-init-databases"
	namespace := "rbd-system"

	// 生成MySQL初始化Job YAML
	yamlContent := fmt.Sprintf(mysqlInitYAML,
		m.config.MySQL.RootPassword, // password for waiting connection
		m.config.MySQL.RootPassword, // password for console database
		m.config.MySQL.RootPassword, // password for region database
		m.config.MySQL.RootPassword, // password for master status
		m.config.MySQL.RootPassword, // password for show databases
		m.config.MySQL.RootPassword, // password for waiting slave
		m.config.MySQL.RootPassword, // password for creating test table
		m.config.MySQL.RootPassword, // password for data sync verification
		m.config.MySQL.RootPassword, // password for cleanup test table
	)

	// 使用Kubernetes API创建资源
	if err := m.applyYAMLOnFirstNode(yamlContent, "MySQL 初始化Job"); err != nil {
		return err
	}

	if m.logger != nil {
		m.logger.Info("等待数据库初始化完成，实时显示日志...")
	}

	// 等待Job的Pod启动并获取日志
	var podName string
	for i := 0; i < 30; i++ { // 等待Pod创建
		pods, err := m.kubeClient.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
			LabelSelector: "job-name=mysql-init-databases",
		})
		if err == nil && len(pods.Items) > 0 {
			podName = pods.Items[0].Name
			if m.logger != nil {
				m.logger.Info("找到初始化Pod: %s", podName)
			}
			break
		}
		if i == 29 {
			return fmt.Errorf("等待初始化Pod创建超时")
		}
		time.Sleep(2 * time.Second)
	}

	// 简化版本：定期检查Job状态
	if m.logger != nil {
		m.logger.Info("=== 监控MySQL初始化任务进度 ===")
	}

	// 等待Job完成
	jobCompleted := false
	for i := 0; i < 30; i++ { // 最多等待5分钟
		job, err := m.kubeClient.BatchV1().Jobs(namespace).Get(context.TODO(), jobName, metav1.GetOptions{})
		if err == nil {
			if job.Status.Succeeded > 0 {
				jobCompleted = true
				break
			}
			if job.Status.Failed > 0 {
				return fmt.Errorf("数据库初始化Job执行失败")
			}
			if m.logger != nil && i%3 == 0 { // 每30秒输出一次进度
				m.logger.Info("初始化任务进行中... (%d/5分钟)", (i+1)*10)
			}
		}

		time.Sleep(10 * time.Second)
	}

	if !jobCompleted {
		return fmt.Errorf("数据库初始化超时")
	}

	if m.logger != nil {
		m.logger.Info("=== MySQL初始化完成 ===")
	}
	return nil
}

func (m *MySQLInstaller) verifyDeployment() error {
	if m.logger != nil {
		m.logger.Info("验证MySQL部署状态...")
	}

	namespace := "rbd-system"

	// 检查Master状态
	masterPods, err := m.kubeClient.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: "app=mysql-master",
	})
	if err != nil {
		return fmt.Errorf("检查MySQL Master状态失败: %w", err)
	}

	if m.logger != nil {
		m.logger.Info("MySQL Master状态:")
		for _, pod := range masterPods.Items {
			m.logger.Info("  Pod: %s, 状态: %s", pod.Name, pod.Status.Phase)
		}
	}

	// 如果有Slave节点，检查Slave状态
	if m.hasSlaveNode() {
		slavePods, err := m.kubeClient.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
			LabelSelector: "app=mysql-slave",
		})
		if err != nil {
			return fmt.Errorf("检查MySQL Slave状态失败: %w", err)
		}

		if m.logger != nil {
			m.logger.Info("MySQL Slave状态:")
			for _, pod := range slavePods.Items {
				m.logger.Info("  Pod: %s, 状态: %s", pod.Name, pod.Status.Phase)
			}
		}
	}

	// 检查Service状态
	services, err := m.kubeClient.CoreV1().Services(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: "app=mysql-master",
	})
	if err != nil {
		return fmt.Errorf("检查MySQL服务状态失败: %w", err)
	}

	if m.logger != nil {
		m.logger.Info("MySQL服务状态:")
		for _, svc := range services.Items {
			m.logger.Info("  Service: %s, ClusterIP: %s", svc.Name, svc.Spec.ClusterIP)
		}
	}

	return nil
}

func (m *MySQLInstaller) buildSSHCommand(host config.Host, command string) *exec.Cmd {
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

func (m *MySQLInstaller) getMasterHost() *config.Host {
	for i := range m.config.Hosts {
		if m.config.Hosts[i].MySQLMaster {
			return &m.config.Hosts[i]
		}
	}
	return nil
}

func (m *MySQLInstaller) getSlaveHost() *config.Host {
	for i := range m.config.Hosts {
		if m.config.Hosts[i].MySQLSlave {
			return &m.config.Hosts[i]
		}
	}
	return nil
}

func (m *MySQLInstaller) hasSlaveNode() bool {
	return m.getSlaveHost() != nil
}

func (m *MySQLInstaller) createNamespace() error {
	if m.logger != nil {
		m.logger.Info("创建rbd-system命名空间...")
	}

	// 确保Kubernetes客户端已初始化
	if m.kubeClient == nil {
		if err := m.initializeKubeClient(); err != nil {
			return fmt.Errorf("初始化Kubernetes客户端失败: %w", err)
		}
	}

	// 使用Kubernetes API检查命名空间是否已存在
	_, err := m.kubeClient.CoreV1().Namespaces().Get(context.TODO(), "rbd-system", metav1.GetOptions{})
	if err == nil {
		if m.logger != nil {
			m.logger.Info("命名空间rbd-system已存在，跳过创建")
		}
		return nil
	}

	// 创建命名空间
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rbd-system",
		},
	}

	_, err = m.kubeClient.CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("创建命名空间失败: %w", err)
	}

	if m.logger != nil {
		m.logger.Info("命名空间rbd-system创建成功")
	}
	return nil
}

func (m *MySQLInstaller) applyYAMLOnFirstNode(yamlContent, component string) error {
	if m.logger != nil {
		m.logger.Info("部署%s...", component)
	}

	// 确保Kubernetes客户端已初始化
	if m.kubeClient == nil {
		if err := m.initializeKubeClient(); err != nil {
			return fmt.Errorf("初始化Kubernetes客户端失败: %w", err)
		}
	}

	// 使用Kubernetes API解析和创建资源
	if err := m.applyYAMLContent(yamlContent); err != nil {
		return fmt.Errorf("部署%s失败: %w", component, err)
	}

	if m.logger != nil {
		m.logger.Info("%s部署成功", component)
	}
	return nil
}

// applyYAMLContent 解析YAML内容并使用Kubernetes API创建资源
func (m *MySQLInstaller) applyYAMLContent(yamlContent string) error {
	// 创建解码器
	decoder := serializer.NewCodecFactory(scheme.Scheme).UniversalDeserializer()
	
	// 按文档分割YAML内容
	docs := strings.Split(yamlContent, "---")
	
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" || strings.HasPrefix(doc, "#") {
			continue
		}

		// 解析YAML文档
		obj, gvk, err := decoder.Decode([]byte(doc), nil, nil)
		if err != nil {
			if m.logger != nil {
				m.logger.Warn("跳过无法解析的YAML文档: %v", err)
			}
			continue
		}

		// 根据对象类型创建资源
		if err := m.createKubernetesResource(obj, gvk); err != nil {
			return fmt.Errorf("创建资源失败: %w", err)
		}
	}

	return nil
}

// createKubernetesResource 根据资源类型创建Kubernetes资源
func (m *MySQLInstaller) createKubernetesResource(obj runtime.Object, gvk *schema.GroupVersionKind) error {
	switch gvk.Kind {
	case "Service":
		service := obj.(*corev1.Service)
		return m.createOrUpdateService(service)
	case "StatefulSet":
		statefulSet := obj.(*appsv1.StatefulSet)
		return m.createOrUpdateStatefulSet(statefulSet)
	case "Job":
		job := obj.(*batchv1.Job)
		return m.createOrUpdateJob(job)
	default:
		if m.logger != nil {
			m.logger.Warn("不支持的资源类型: %s", gvk.Kind)
		}
		return nil
	}
}


// waitForPodsReady 等待指定标签的Pod就绪
func (m *MySQLInstaller) waitForPodsReady(labelSelector string, componentName string) error {
	// 确保Kubernetes客户端已初始化
	if m.kubeClient == nil {
		if err := m.initializeKubeClient(); err != nil {
			return fmt.Errorf("初始化Kubernetes客户端失败: %w", err)
		}
	}

	for i := 0; i < 60; i++ { // 最多等待10分钟
		pods, err := m.kubeClient.CoreV1().Pods("rbd-system").List(context.TODO(), metav1.ListOptions{
			LabelSelector: labelSelector,
			FieldSelector: "status.phase=Running",
		})

		if err == nil && len(pods.Items) > 0 {
			if m.logger != nil {
				m.logger.Info("%s已就绪", componentName)
			}
			return nil
		}

		if i == 59 {
			return fmt.Errorf("等待%s就绪超时", componentName)
		}

		time.Sleep(10 * time.Second)
		if m.logger != nil {
			m.logger.Debug("等待%s就绪... (%d/60)", componentName, i+1)
		}
	}

	return nil
}

// createOrUpdateService 创建或更新Service
func (m *MySQLInstaller) createOrUpdateService(service *corev1.Service) error {
	if m.logger != nil {
		m.logger.Debug("创建Service: %s/%s", service.Namespace, service.Name)
	}

	// 检查Service是否已存在
	existingService, err := m.kubeClient.CoreV1().Services(service.Namespace).Get(context.TODO(), service.Name, metav1.GetOptions{})
	if err == nil {
		// Service已存在，更新它
		service.ResourceVersion = existingService.ResourceVersion
		service.Spec.ClusterIP = existingService.Spec.ClusterIP // 保持ClusterIP不变
		_, err = m.kubeClient.CoreV1().Services(service.Namespace).Update(context.TODO(), service, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("更新Service %s失败: %w", service.Name, err)
		}
		if m.logger != nil {
			m.logger.Info("Service %s/%s 已更新", service.Namespace, service.Name)
		}
	} else {
		// Service不存在，创建它
		_, err = m.kubeClient.CoreV1().Services(service.Namespace).Create(context.TODO(), service, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("创建Service %s失败: %w", service.Name, err)
		}
		if m.logger != nil {
			m.logger.Info("Service %s/%s 已创建", service.Namespace, service.Name)
		}
	}

	return nil
}

// createOrUpdateStatefulSet 创建或更新StatefulSet
func (m *MySQLInstaller) createOrUpdateStatefulSet(statefulSet *appsv1.StatefulSet) error {
	if m.logger != nil {
		m.logger.Debug("创建StatefulSet: %s/%s", statefulSet.Namespace, statefulSet.Name)
	}

	// 检查StatefulSet是否已存在
	existingStatefulSet, err := m.kubeClient.AppsV1().StatefulSets(statefulSet.Namespace).Get(context.TODO(), statefulSet.Name, metav1.GetOptions{})
	if err == nil {
		// StatefulSet已存在，更新它
		statefulSet.ResourceVersion = existingStatefulSet.ResourceVersion
		_, err = m.kubeClient.AppsV1().StatefulSets(statefulSet.Namespace).Update(context.TODO(), statefulSet, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("更新StatefulSet %s失败: %w", statefulSet.Name, err)
		}
		if m.logger != nil {
			m.logger.Info("StatefulSet %s/%s 已更新", statefulSet.Namespace, statefulSet.Name)
		}
	} else {
		// StatefulSet不存在，创建它
		_, err = m.kubeClient.AppsV1().StatefulSets(statefulSet.Namespace).Create(context.TODO(), statefulSet, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("创建StatefulSet %s失败: %w", statefulSet.Name, err)
		}
		if m.logger != nil {
			m.logger.Info("StatefulSet %s/%s 已创建", statefulSet.Namespace, statefulSet.Name)
		}
	}

	return nil
}

// createOrUpdateJob 创建或更新Job
func (m *MySQLInstaller) createOrUpdateJob(job *batchv1.Job) error {
	if m.logger != nil {
		m.logger.Debug("创建Job: %s/%s", job.Namespace, job.Name)
	}

	// 对于Job，通常我们需要先删除现有的，然后创建新的
	// 因为Job的spec字段通常是不可变的
	err := m.kubeClient.BatchV1().Jobs(job.Namespace).Delete(context.TODO(), job.Name, metav1.DeleteOptions{})
	if err != nil && !strings.Contains(err.Error(), "not found") {
		if m.logger != nil {
			m.logger.Warn("删除现有Job %s时出错: %v", job.Name, err)
		}
	}

	// 等待Job删除完成
	time.Sleep(2 * time.Second)

	// 创建新的Job
	_, err = m.kubeClient.BatchV1().Jobs(job.Namespace).Create(context.TODO(), job, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("创建Job %s失败: %w", job.Name, err)
	}

	if m.logger != nil {
		m.logger.Info("Job %s/%s 已创建", job.Namespace, job.Name)
	}

	return nil
}
