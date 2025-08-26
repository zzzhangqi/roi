# Rainbond Offline Installer (ROI)

Rainbond Offline Installer 是一个基于 Golang 和 Cobra 构建的命令行工具，为 Rainbond 集群提供**在线**和**离线**两种部署模式。

## 功能特性

- 🚀 **双模部署**: 支持在线和离线两种安装模式
- 🐳 **容器化**: 工具及所有依赖打包为容器镜像
- 🔧 **自动化**: 一键式部署，减少手动操作
- 📋 **环境检测**: 全面的系统环境检测和验证
- 🛠️ **组件管理**: 自动安装 RKE2、Rainbond 等组件

## 支持的组件

- **操作系统**: Ubuntu, CentOS, RHEL, Rocky Linux, openEuler
- **容器运行时**: Containerd (纯二进制安装)
- **Kubernetes**: RKE2 (自动传输官方安装脚本)
- **存储**: LVM 分区管理
- **应用平台**: Rainbond

## 快速开始

### 前置要求

**目标服务器：**
- Linux x86_64 系统 (Ubuntu, CentOS, RHEL, Rocky Linux, openEuler)
- 最少 4GB 内存
- 最少 2 核 CPU
- 50GB+ 可用磁盘空间
- Root 权限

**运行 ROI 的本地机器：**
- 任意操作系统 (Linux, macOS, Windows)
- SSH 客户端
- 配置好的 SSH 密钥或 sshpass (用于密码认证)

### SSH 配置

**推荐使用 SSH 密钥认证：**

```bash
# 生成 SSH 密钥对
ssh-keygen -t rsa -b 4096

# 将公钥复制到目标服务器
ssh-copy-id root@192.168.1.10
ssh-copy-id root@192.168.1.11
ssh-copy-id root@192.168.1.12

# 测试连接
ssh root@192.168.1.10 'echo "Connection successful"'
```

**如果使用密码认证：**

```bash
# macOS 安装 sshpass
brew install hudochenkov/sshpass/sshpass

# Ubuntu/Debian 安装 sshpass
sudo apt-get install sshpass

# CentOS/RHEL 安装 sshpass
sudo yum install sshpass
```

### 使用 Docker 运行 (推荐)

```bash
# 拉取镜像
docker pull rainbond-installer:latest

# 准备配置文件
cp examples/config.yaml ./config.yaml
# 编辑配置文件...

# 运行安装
docker run -it --rm --privileged \
  -v /:/host \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v $(pwd)/config.yaml:/config.yaml \
  rainbond-installer:latest install --config /config.yaml
```

### 二进制安装

```bash
# 下载并安装
wget https://github.com/rainbond/rainbond-offline-installer/releases/latest/download/roi-linux-amd64
chmod +x roi-linux-amd64
sudo mv roi-linux-amd64 /usr/local/bin/roi

# 运行安装
roi install --config config.yaml
```

## 配置文件

创建 `config.yaml` 配置文件：

```yaml
hosts:
  # 第一个节点：etcd节点（必须包含etcd，专用etcd存储）
  - ip: 192.168.1.10
    user: root
    ssh_key: ~/.ssh/id_rsa
    role: etcd  # 专用etcd节点
    lvm_config:
      vg_name: vg_rainbond
      pv_devices: ["/dev/sdb", "/dev/sdc"]
      lvs:
        - lv_name: lv_rke2
          size: 100G
          mount_point: /var/lib/rancher/rke2

  # 第二个节点：master节点（专用control-plane）
  - ip: 192.168.1.11
    user: root  
    ssh_key: ~/.ssh/id_rsa
    role: master  # 专用control-plane节点
    lvm_config:
      vg_name: vg_rainbond
      pv_devices: ["/dev/sdb"]
      lvs:
        - lv_name: lv_rke2
          size: 50G
          mount_point: /var/lib/rancher/rke2

  # 第三个节点：worker节点（运行业务负载）
  - ip: 192.168.1.12
    user: root
    ssh_key: ~/.ssh/id_rsa  
    role: worker  # 工作节点
    lvm_config:
      vg_name: vg_rainbond
      pv_devices: ["/dev/sdb"]
      lvs:
        - lv_name: lv_rke2
          size: 50G
          mount_point: /var/lib/rancher/rke2

rainbond:
  version: "5.17.0"
  namespace: "rbd-system"
```

### RKE2 节点角色说明

**单角色配置：**
- **etcd**: 专用 etcd 存储节点（禁用 control-plane）
- **master**: 专用 control-plane 节点（禁用 etcd）
- **worker**: 工作节点（运行业务负载）

**多角色配置：**
支持逗号分隔的多角色配置，如：
- **master,etcd**: 混合节点（同时运行 etcd 和 control-plane）
- **master,etcd,worker**: 全功能节点（适合小型集群）

**配置示例：**
```yaml
hosts:
  - ip: 192.168.1.10
    role: master,etcd     # 混合节点
  - ip: 192.168.1.11  
    role: worker          # 纯工作节点
  - ip: 192.168.1.12
    role: master,etcd,worker  # 全功能节点
```

## 命令参考

### 配置预览

```bash
roi preview --config config.yaml
```

预览将要生成的配置文件和安装步骤，无需实际连接服务器，帮助您在真实安装前验证配置是否符合预期。

### 环境检测

```bash
roi check --config config.yaml
```

检查系统环境、硬件要求、网络连接等。

### 系统初始化

```bash
roi init --config config.yaml
```

初始化系统环境，包括：
- 禁用 SWAP
- 配置防火墙
- 设置内核参数
- 创建 LVM 分区
- 安装 Docker

### RKE2 Kubernetes 集群安装

```bash
roi install --rke2 --config config.yaml
```

安装和配置 RKE2 Kubernetes 集群，支持三种节点角色：
- **etcd**: 专用 etcd 节点（第一个节点必须包含 etcd）
- **master**: 专用 control-plane 节点（apiserver、controller-manager、scheduler）
- **worker**: 工作节点（运行业务负载）

特性：
- 自动传输 RKE2 安装脚本到目标服务器
- 根据节点角色生成专用配置
- 禁用默认 Ingress（避免与 Rainbond 冲突）
- 配置中国镜像源加速

### 完整安装

```bash
roi install --config config.yaml
```

执行完整的 Rainbond 集群安装，包括：
- 系统环境检测
- LVM 分区配置
- RKE2 Kubernetes 集群部署
- 系统优化配置
- Rainbond 平台安装

## 安装模式

### 在线模式

在线模式下，工具会从互联网下载所需的组件和镜像：

```yaml
general:
  installation_mode: online
  registry:
    address: registry.example.com
    username: user
    password: pass
```

### 离线模式

离线模式下，工具使用预打包的资源和内置镜像仓库：

```yaml
general:
  installation_mode: offline
  offline_registry:
    port: 5000
```

## 开发指南

### 构建项目

```bash
# 构建二进制
make build

# 构建 Docker 镜像
make docker-build

# 运行测试
make test

# 代码检查
make lint
```

### 准备离线资源

```bash
# 创建资源目录
make prepare-offline

# 手动下载资源到 resources/ 目录
# - RKE2 二进制和镜像
# - Helm 二进制
# - Rainbond Charts 和镜像
# - MySQL 镜像
```

### 项目结构

```
├── cmd/                    # 命令行入口
│   ├── main.go
│   └── roi/               # CLI 命令实现
├── pkg/                   # 公共包
│   ├── config/           # 配置管理
│   ├── installer/        # 组件安装器
│   └── resource/         # 资源管理
├── internal/             # 内部包
│   ├── check/           # 环境检测
│   ├── init/            # 系统初始化
│   └── install/         # 安装协调器
├── examples/            # 配置示例
├── resources/           # 离线资源（需手动准备）
├── Dockerfile
├── docker-compose.yml
└── Makefile
```

## 故障排除

### 常见问题

1. **权限错误**: 确保以 root 用户运行或使用 `sudo`
2. **网络连接失败**: 检查防火墙设置和网络配置
3. **磁盘空间不足**: 确保有足够的磁盘空间
4. **Docker 守护进程未运行**: 启动 Docker 服务

### 日志查看

```bash
# 查看详细输出
roi install --config config.yaml --verbose

# 查看系统日志
journalctl -u rke2-server
journalctl -u docker

# 查看 Kubernetes 日志
kubectl logs -n rbd-system <pod-name>
```

## 贡献指南

1. Fork 本项目
2. 创建特性分支 (`git checkout -b feature/amazing-feature`)
3. 提交更改 (`git commit -m 'Add amazing feature'`)
4. 推送到分支 (`git push origin feature/amazing-feature`)
5. 创建 Pull Request

## 许可证

本项目采用 [Apache 2.0](LICENSE) 许可证。

## 支持

- 📖 [文档](https://www.rainbond.com/docs/)
- 💬 [社区论坛](https://t.goodrain.com/)
- 🐛 [问题反馈](https://github.com/rainbond/rainbond-offline-installer/issues)
- 📧 [邮件支持](mailto:support@goodrain.com)