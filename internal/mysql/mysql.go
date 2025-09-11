package mysql

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rainbond/rainbond-offline-installer/pkg/config"
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
}

func NewMySQLInstaller(cfg *config.Config) *MySQLInstaller {
	return NewMySQLInstallerWithLogger(cfg, nil)
}

func NewMySQLInstallerWithLogger(cfg *config.Config, logger Logger) *MySQLInstaller {
	return NewMySQLInstallerWithLoggerAndProgress(cfg, logger, nil)
}

func NewMySQLInstallerWithLoggerAndProgress(cfg *config.Config, logger Logger, stepProgress StepProgress) *MySQLInstaller {
	return &MySQLInstaller{
		config:       cfg,
		logger:       logger,
		stepProgress: stepProgress,
	}
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

	cmd := m.buildSSHCommand(m.config.Hosts[0], "kubectl get nodes")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl命令执行失败: %w, 输出: %s", err, string(output))
	}

	if strings.Contains(string(output), "Ready") {
		if m.logger != nil {
			m.logger.Info("Kubernetes集群已就绪")
		}
		return nil
	}

	return fmt.Errorf("Kubernetes集群未就绪")
}

func (m *MySQLInstaller) checkExistingDeployment() (bool, error) {
	if m.logger != nil {
		m.logger.Info("检查现有MySQL部署...")
	}

	cmd := m.buildSSHCommand(m.config.Hosts[0], "kubectl get statefulset mysql-master -n rbd-system")
	err := cmd.Run()
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

	// 在第一个节点上执行kubectl apply
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

	// 在第一个节点上执行kubectl apply
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
	for i := 0; i < 60; i++ { // 最多等待10分钟
		cmd := m.buildSSHCommand(m.config.Hosts[0], "kubectl get pod -l app=mysql-master -n rbd-system --field-selector=status.phase=Running")
		output, err := cmd.Output()
		if err == nil && len(strings.TrimSpace(string(output))) > 0 {
			if m.logger != nil {
				m.logger.Info("MySQL Master已就绪")
			}
			break
		}

		if i == 59 {
			return fmt.Errorf("等待MySQL Master就绪超时")
		}

		time.Sleep(10 * time.Second)
		if m.logger != nil {
			m.logger.Debug("等待MySQL Master就绪... (%d/60)", i+1)
		}
	}

	// 如果有Slave节点，等待Slave就绪
	if m.hasSlaveNode() {
		if m.logger != nil {
			m.logger.Info("等待MySQL Slave就绪...")
		}
		for i := 0; i < 60; i++ { // 最多等待10分钟
			cmd := m.buildSSHCommand(m.config.Hosts[0], "kubectl get pod -l app=mysql-slave -n rbd-system --field-selector=status.phase=Running")
			output, err := cmd.Output()
			if err == nil && len(strings.TrimSpace(string(output))) > 0 {
				if m.logger != nil {
				m.logger.Info("MySQL Slave已就绪")
			}
				break
			}

			if i == 59 {
				return fmt.Errorf("等待MySQL Slave就绪超时")
			}

			time.Sleep(10 * time.Second)
			if m.logger != nil {
				m.logger.Debug("等待MySQL Slave就绪... (%d/60)", i+1)
			}
		}
	}

	return nil
}

func (m *MySQLInstaller) initializeDatabases() error {
	if m.logger != nil {
		m.logger.Info("初始化MySQL数据库...")
	}

	// 先删除可能存在的Job（因为Job的spec.template字段不可变）
	if m.logger != nil {
		m.logger.Info("清理可能存在的MySQL初始化Job...")
	}
	deleteCmd := m.buildSSHCommand(m.config.Hosts[0], "kubectl delete job mysql-init-databases -n rbd-system --ignore-not-found=true")
	if err := deleteCmd.Run(); err != nil {
		if m.logger != nil {
			m.logger.Warn("删除现有Job失败，但继续执行: %v", err)
		}
	}

	// 等待Job删除完成
	if m.logger != nil {
		m.logger.Info("等待Job删除完成...")
	}
	for i := 0; i < 30; i++ {
		checkCmd := m.buildSSHCommand(m.config.Hosts[0], "kubectl get job mysql-init-databases -n rbd-system")
		if err := checkCmd.Run(); err != nil {
			// Job不存在，可以继续
			break
		}
		if i == 29 {
			if m.logger != nil {
				m.logger.Warn("等待Job删除超时，但继续执行")
			}
		}
		time.Sleep(2 * time.Second)
	}

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

	// 在第一个节点上执行kubectl apply
	if err := m.applyYAMLOnFirstNode(yamlContent, "MySQL 初始化Job"); err != nil {
		return err
	}

	if m.logger != nil {
		m.logger.Info("等待数据库初始化完成，实时显示日志...")
	}

	// 等待Job的Pod启动并获取日志
	var podName string
	for i := 0; i < 30; i++ { // 等待Pod创建
		cmd := m.buildSSHCommand(m.config.Hosts[0], "kubectl get pods -l job-name=mysql-init-databases -n rbd-system -o jsonpath='{.items[0].metadata.name}' 2>/dev/null")
		output, err := cmd.Output()
		if err == nil && strings.TrimSpace(string(output)) != "" {
			podName = strings.TrimSpace(string(output))
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

	// 实时流式输出日志
	if m.logger != nil {
		m.logger.Info("=== MySQL初始化日志 ===")
	}
	logCmd := m.buildSSHCommand(m.config.Hosts[0], fmt.Sprintf("kubectl logs -f %s -n rbd-system", podName))

	// 启动日志流
	logCmd.Stdout = nil // 清除默认输出
	logCmd.Stderr = nil

	// 获取输出管道
	stdout, err := logCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("获取日志输出管道失败: %w", err)
	}

	// 启动日志命令
	if err := logCmd.Start(); err != nil {
		return fmt.Errorf("启动日志命令失败: %w", err)
	}

	// 在goroutine中实时读取和打印日志
	logDone := make(chan bool)
	go func() {
		for {
			// 读取日志输出
			buffer := make([]byte, 1024)
			n, err := stdout.Read(buffer)
			if err != nil {
				if err.Error() != "EOF" {
					if m.logger != nil {
						m.logger.Warn("读取日志失败: %v", err)
					}
				}
				break
			}
			if n > 0 {
				// 打印到控制台（移除logger前缀，直接输出）
				fmt.Print(string(buffer[:n]))
			}
		}
		logDone <- true
	}()

	// 等待Job完成
	jobCompleted := false
	for i := 0; i < 30; i++ { // 最多等待5分钟
		cmd := m.buildSSHCommand(m.config.Hosts[0], "kubectl get job mysql-init-databases -n rbd-system -o jsonpath='{.status.succeeded}'")
		output, err := cmd.Output()
		if err == nil && strings.TrimSpace(string(output)) == "1" {
			jobCompleted = true
			break
		}

		// 检查Job是否失败
		cmd = m.buildSSHCommand(m.config.Hosts[0], "kubectl get job mysql-init-databases -n rbd-system -o jsonpath='{.status.failed}'")
		output, err = cmd.Output()
		if err == nil && strings.TrimSpace(string(output)) == "1" {
			logCmd.Process.Kill()
			<-logDone
			return fmt.Errorf("数据库初始化Job执行失败")
		}

		time.Sleep(10 * time.Second)
	}

	// 终止日志流
	if logCmd.Process != nil {
		logCmd.Process.Kill()
	}
	<-logDone

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

	// 检查Master状态
	cmd := m.buildSSHCommand(m.config.Hosts[0], "kubectl get pods -l app=mysql-master -n rbd-system")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("检查MySQL Master状态失败: %w", err)
	}

	if m.logger != nil {
		m.logger.Info("MySQL Master状态:")
		m.logger.Info(string(output))
	}

	// 如果有Slave节点，检查Slave状态
	if m.hasSlaveNode() {
		cmd = m.buildSSHCommand(m.config.Hosts[0], "kubectl get pods -l app=mysql-slave -n rbd-system")
		output, err = cmd.Output()
		if err != nil {
			return fmt.Errorf("检查MySQL Slave状态失败: %w", err)
		}

		if m.logger != nil {
			m.logger.Info("MySQL Slave状态:")
			m.logger.Info(string(output))
		}
	}

	// 检查Service状态
	cmd = m.buildSSHCommand(m.config.Hosts[0], "kubectl get svc -l app=mysql-master -n rbd-system")
	output, err = cmd.Output()
	if err != nil {
		return fmt.Errorf("检查MySQL服务状态失败: %w", err)
	}

	if m.logger != nil {
		m.logger.Info("MySQL服务状态:")
		m.logger.Info(string(output))
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

	// 检查命名空间是否已存在
	cmd := m.buildSSHCommand(m.config.Hosts[0], "kubectl get namespace rbd-system")
	if err := cmd.Run(); err == nil {
		if m.logger != nil {
			m.logger.Info("命名空间rbd-system已存在，跳过创建")
		}
		return nil
	}

	// 创建命名空间
	cmd = m.buildSSHCommand(m.config.Hosts[0], "kubectl create namespace rbd-system")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("创建命名空间失败: %w, 输出: %s", err, string(output))
	}

	if m.logger != nil {
		m.logger.Info("命名空间rbd-system创建成功")
	}
	return nil
}

func (m *MySQLInstaller) applyYAMLOnFirstNode(yamlContent, component string) error {
	if m.logger != nil {
		m.logger.Info("在第一个节点上部署%s...", component)
	}

	// 将YAML内容写入临时文件
	tempFile := fmt.Sprintf("/tmp/mysql-%s.yaml", strings.ToLower(strings.ReplaceAll(component, " ", "-")))
	writeCmd := fmt.Sprintf("cat > %s << 'EOF'\n%s\nEOF", tempFile, yamlContent)

	// 先写入YAML文件
	cmd := m.buildSSHCommand(m.config.Hosts[0], writeCmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("写入%s YAML文件失败: %w", component, err)
	}

	// 然后执行kubectl apply
	applyCmd := fmt.Sprintf("kubectl apply -f %s", tempFile)
	cmd = m.buildSSHCommand(m.config.Hosts[0], applyCmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("部署%s失败: %w, 输出: %s", component, err, string(output))
	}

	// 清理临时文件
	cleanCmd := fmt.Sprintf("rm -f %s", tempFile)
	cmd = m.buildSSHCommand(m.config.Hosts[0], cleanCmd)
	cmd.Run() // 忽略清理错误

	if m.logger != nil {
		m.logger.Info("%s部署成功", component)
	}
	return nil
}
